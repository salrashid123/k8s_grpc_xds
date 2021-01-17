package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	v2route "github.com/envoyproxy/go-control-plane/envoy/api/v2/route"

	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	lv2 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v2"

	ep "github.com/envoyproxy/go-control-plane/envoy/api/v2/endpoint"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v2"
	xds "github.com/envoyproxy/go-control-plane/pkg/server/v2"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	wrapperspb "github.com/golang/protobuf/ptypes/wrappers"

	hcm "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/http_connection_manager/v2"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v2"
	"github.com/golang/protobuf/ptypes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/client-go/kubernetes"
)

var (
	debug           bool
	port            uint
	refreshInterval uint

	version int32

	config cache.SnapshotCache
	// clientSet *kubernetes.Clientset
)

type ServiceConfigs struct {
	ServiceConfig []ServiceConfig `json:"services"`
}
type ServiceConfig struct {
	ServiceName     string `json:"serviceName"`
	NameSpace       string `json:"namespace"`
	PortName        string `json:"portName"`
	Protocol        string `json:"protocol"`
	GrpcServiceName string `json:"grpcServiceName"`
	Zone            string `json:"zone"`
	Region          string `json:"region"`
}

const ()

func init() {
	flag.BoolVar(&debug, "debug", true, "Use debug logging")
	flag.UintVar(&port, "port", 18000, "Management server port")
	flag.UintVar(&refreshInterval, "refreshInterval", 10, "Refresh valid service list")
}

type logger struct{}

func (logger logger) Infof(format string, args ...interface{}) {
	log.Infof(format, args...)
}
func (logger logger) Errorf(format string, args ...interface{}) {
	log.Errorf(format, args...)
}
func (cb *callbacks) Report() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	log.WithFields(log.Fields{"fetches": cb.fetches, "requests": cb.requests}).Info("cb.Report()  callbacks")
}
func (cb *callbacks) OnStreamOpen(ctx context.Context, id int64, typ string) error {
	log.Infof("OnStreamOpen %d open for Type [%s]", id, typ)
	return nil
}
func (cb *callbacks) OnStreamClosed(id int64) {
	log.Infof("OnStreamClosed %d closed", id)
}
func (cb *callbacks) OnStreamRequest(id int64, r *v2.DiscoveryRequest) error {
	log.Infof("OnStreamRequest %d  Request[%v]", id, r.TypeUrl)
	log.Infof("OnStreamRequest %d  Request[%v]", id, r)
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.requests++
	if cb.signal != nil {
		close(cb.signal)
		cb.signal = nil
	}
	return nil
}
func (cb *callbacks) OnStreamResponse(id int64, req *v2.DiscoveryRequest, resp *v2.DiscoveryResponse) {
	log.Infof("OnStreamResponse... %d   Request [%v],  Response[%v]", id, req.TypeUrl, resp.TypeUrl)
	cb.Report()
}
func (cb *callbacks) OnFetchRequest(ctx context.Context, req *v2.DiscoveryRequest) error {
	log.Infof("OnFetchRequest... Request [%v]", req.TypeUrl)
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.fetches++
	if cb.signal != nil {
		close(cb.signal)
		cb.signal = nil
	}
	return nil
}
func (cb *callbacks) OnFetchResponse(req *v2.DiscoveryRequest, resp *v2.DiscoveryResponse) {
	log.Infof("OnFetchResponse... Resquest[%v],  Response[%v]", req.TypeUrl, resp.TypeUrl)
}

type callbacks struct {
	signal   chan struct{}
	fetches  int
	requests int
	mu       sync.Mutex
}

// Hasher returns node ID as an ID
type Hasher struct {
}

// ID function
func (h Hasher) ID(node *core.Node) string {
	if node == nil {
		return "unknown"
	}
	return node.Id
}

const grpcMaxConcurrentStreams = 1000

// RunManagementServer starts an xDS server at the given port.
func RunManagementServer(ctx context.Context, server xds.Server, port uint) {
	var grpcOptions []grpc.ServerOption
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams))
	grpcServer := grpc.NewServer(grpcOptions...)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.WithError(err).Fatal("failed to listen")
	}

	// register services
	discovery.RegisterAggregatedDiscoveryServiceServer(grpcServer, server)
	v2.RegisterEndpointDiscoveryServiceServer(grpcServer, server)
	v2.RegisterClusterDiscoveryServiceServer(grpcServer, server)
	v2.RegisterRouteDiscoveryServiceServer(grpcServer, server)
	v2.RegisterListenerDiscoveryServiceServer(grpcServer, server)

	log.WithFields(log.Fields{"port": port}).Info("management server listening")
	go func() {
		if err = grpcServer.Serve(lis); err != nil {
			log.Error(err)
		}
	}()
	<-ctx.Done()

	grpcServer.GracefulStop()
}

func main() {
	flag.Parse()
	if debug {
		log.SetLevel(log.DebugLevel)
	}
	ctx := context.Background()

	log.Printf("Starting control plane")

	// k8sconfig, err := rest.InClusterConfig()
	// if err != nil {
	// 	log.Fatal(err.Error())
	// }

	// clientSet, err = kubernetes.NewForConfig(k8sconfig)
	// if err != nil {
	// 	log.Fatal(err.Error())
	// }

	signal := make(chan struct{})
	cb := &callbacks{
		signal:   signal,
		fetches:  0,
		requests: 0,
	}
	config = cache.NewSnapshotCache(true, cache.IDHash{}, nil)

	srv := xds.NewServer(ctx, config, cb)

	go RunManagementServer(ctx, srv, port)

	<-signal

	cb.Report()

	nodeId := config.GetStatusKeys()[0]
	log.Infof(">>>>>>>>>>>>>>>>>>> creating NodeID %s", nodeId)
	for {

		// ctx := context.Background()
		// services, err := clientSet.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		// 	//FieldSelector: "metadata.name=" + serviceName,
		// })
		// if err != nil {
		// 	log.Printf("Get service from kubernetes cluster error:%v", err)
		// 	return
		// }

		// for _, svc := range services.Items {
		// 	log.Println("found Service %v", svc.Name)
		// }

		var wg sync.WaitGroup

		jsonFile, err := os.Open("config/svc_config.json")

		if err != nil {
			log.Errorf("Could not read svc_config", err.Error())
		}
		log.Printf("Successfully Opened svc_config.json")
		// defer the closing of our jsonFile so that we can parse it later on
		defer jsonFile.Close()

		byteValue, _ := ioutil.ReadAll(jsonFile)

		var tx ServiceConfigs
		err = json.Unmarshal(byteValue, &tx)
		if err != nil {
			log.Errorf("Could not read svc_config.json", err.Error())
		}

		rt := []types.Resource{}
		sec := []types.Resource{}
		eds := []types.Resource{}
		cls := []types.Resource{}
		rds := []types.Resource{}
		lsnr := []types.Resource{}

		for _, svcc := range tx.ServiceConfig {
			log.Printf("%s", svcc.ServiceName)

			serviceName := svcc.ServiceName         //"be-srv"
			namespace := svcc.NameSpace             //"default"
			portName := svcc.PortName               //"grpc"
			protocol := svcc.Protocol               //"tcp"
			grpcServiceName := svcc.GrpcServiceName //"echo.EchoServer"
			region := svcc.Region                   //"us-central1"
			zone := svcc.Zone                       // us-central1-a

			routeConfigName := serviceName + "-route"
			clusterName := serviceName + "-cluster"
			virtualHostName := serviceName + "-vs"
			var upstreamPorts []string

			cname, rec, err := net.LookupSRV(portName, protocol, fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace))
			if err != nil {
				log.Errorf("Could not find server %s", serviceName, err.Error())
				break
			} else {
				log.Printf("SRV CNAME: %v\n", cname)
			}

			for i := range rec {
				wg.Add(1)
				go func(host string, port string) {
					defer wg.Done()
					address := fmt.Sprintf("%s:%s", host, port)

					ctx := context.Background()
					ctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
					defer cancel()
					conn, err := grpc.Dial(address, grpc.WithInsecure())
					if err != nil {
						log.Errorf("Could not connect to endpoint %s  %v", address, err.Error())
						return
					}
					resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: grpcServiceName})
					if err != nil {
						log.Errorf("HealthCheck failed %v", conn, err.Error())
						return
					}
					if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
						log.Errorf("Service not healthy %v %v", conn, fmt.Sprintf("service not in serving state: %v", resp.GetStatus().String()))
						return
					}
					log.Printf("RPC HealthChekStatus: for %v %v", address, resp.GetStatus())
					upstreamPorts = append(upstreamPorts, address)
				}(rec[i].Target, strconv.Itoa(int(rec[i].Port)))
			}
			wg.Wait()
			log.Printf("ClusterIPs: %v", upstreamPorts)

			// now update the xds endpoints

			var lbe []*ep.LbEndpoint

			for _, v := range upstreamPorts {
				backendHostName := strings.Split(v, ":")[0]
				backendPort := strings.Split(v, ":")[1]
				uPort, err := strconv.ParseUint(backendPort, 10, 32)
				if err != nil {
					log.Errorf("Could not parse port %v", err)
					break
				}
				// ENDPOINT
				log.Infof(">>>>>>>>>>>>>>>>>>> creating ENDPOINT for remoteHost:port %s:%s", backendHostName, backendPort)
				hst := &core.Address{Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						Address:  backendHostName,
						Protocol: core.SocketAddress_TCP,
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: uint32(uPort),
						},
					},
				}}

				ee := &ep.LbEndpoint{
					HostIdentifier: &ep.LbEndpoint_Endpoint{
						Endpoint: &ep.Endpoint{
							Address: hst,
						}},
					HealthStatus: core.HealthStatus_HEALTHY,
				}
				lbe = append(lbe, ee)
			}
			eds = append(eds, &v2.ClusterLoadAssignment{
				ClusterName: clusterName,
				Endpoints: []*ep.LocalityLbEndpoints{{
					Locality: &core.Locality{
						Region: region,
						Zone:   zone,
					},
					Priority:            0,
					LoadBalancingWeight: &wrapperspb.UInt32Value{Value: uint32(1000)},
					LbEndpoints:         lbe,
				}},
			})

			// CLUSTER
			log.Infof(">>>>>>>>>>>>>>>>>>> creating CLUSTER " + clusterName)

			cls = append(cls, &v2.Cluster{
				Name:                 clusterName,
				LbPolicy:             v2.Cluster_ROUND_ROBIN,
				ClusterDiscoveryType: &v2.Cluster_Type{Type: v2.Cluster_EDS},
				EdsClusterConfig: &v2.Cluster_EdsClusterConfig{
					EdsConfig: &core.ConfigSource{
						ConfigSourceSpecifier: &core.ConfigSource_Ads{},
					},
				},
			})

			// RDS
			log.Infof(">>>>>>>>>>>>>>>>>>> creating RDS " + virtualHostName)
			vh := &v2route.VirtualHost{
				Name:    virtualHostName,
				Domains: []string{serviceName}, //******************* >> must match what is specified at xds:/// //

				Routes: []*v2route.Route{{
					Match: &v2route.RouteMatch{
						PathSpecifier: &v2route.RouteMatch_Prefix{
							Prefix: "",
						},
					},
					Action: &v2route.Route_Route{
						Route: &v2route.RouteAction{
							ClusterSpecifier: &v2route.RouteAction_Cluster{
								Cluster: clusterName,
							},
						},
					},
				}}}

			rds = append(rds, &v2.RouteConfiguration{
				Name:         routeConfigName,
				VirtualHosts: []*v2route.VirtualHost{vh},
			})
			// LISTENER
			log.Infof(">>>>>>>>>>>>>>>>>>> creating LISTENER " + serviceName)
			hcRds := &hcm.HttpConnectionManager_Rds{
				Rds: &hcm.Rds{
					RouteConfigName: routeConfigName,
					ConfigSource: &core.ConfigSource{
						ConfigSourceSpecifier: &core.ConfigSource_Ads{
							Ads: &core.AggregatedConfigSource{},
						},
					},
				},
			}

			manager := &hcm.HttpConnectionManager{
				CodecType:      hcm.HttpConnectionManager_AUTO,
				RouteSpecifier: hcRds,
			}

			pbst, err := ptypes.MarshalAny(manager)
			if err != nil {
				panic(err)
			}

			lsnr = append(lsnr, &v2.Listener{
				Name: serviceName,
				ApiListener: &lv2.ApiListener{
					ApiListener: pbst,
				},
			})
		}
		// =================================================================================
		atomic.AddInt32(&version, 1)
		log.Infof(" creating snapshot Version " + fmt.Sprint(version))

		log.Infof("   snapshot with Listener %v", lsnr)
		log.Infof("   snapshot with EDS %v", eds)
		log.Infof("   snapshot with CLS %v", cls)
		log.Infof("   snapshot with RDS %v", rds)

		snap := cache.NewSnapshot(fmt.Sprint(version), eds, cls, rds, lsnr, rt, sec)
		err = config.SetSnapshot(nodeId, snap)
		if err != nil {
			log.Printf(">>>>>>>>>>  Error setting snapshot %v", err)
		}
		time.Sleep(time.Duration(refreshInterval) * time.Second)
	}
}
