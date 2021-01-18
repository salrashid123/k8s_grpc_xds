## Kubernetes xDS service for gRPC loadbalancing

Kubernetes service for gRPC xDS loadbalancing that allows even distribution of k8s `gRPC service->service` api calls.

gRPC loadbalancinng can take many different schemes as described here [Load Balancing in gRPC](https://github.com/grpc/grpc/blob/master/doc/load-balancing.md).  The particular scheme described here is pretty unique and experimental: [xDS server for gRPC](https://github.com/salrashid123/grpc_xds).

Note, normally [kubernetes services](https://kubernetes.io/docs/concepts/services-networking/service/) are exposed as a single destination endpoint where clients connect to.  Kubernetes will basically proxy a connection from one client to one destination pod to handle any given request. This isn't usually a problem for HTTP/REST clients since its a simple request/response model.  For gRPC, however, one grpc connection that terminates at a pod can send many 100's of individual rpcs on that one conn.  This will cause that destination pod to quickly get overwhelmed.

There are several ways to mitigate this for gRPC by running a proxy that is 'gRPC Aware' in the sense that it can distribute each rpc:

- [Using Istio to load-balance internal gRPC services](https://cloud.google.com/solutions/using-istio-for-internal-load-balancing-of-grpc-services)
- [Using Envoy Proxy to load-balance gRPC services on GKE](https://cloud.google.com/solutions/exposing-grpc-services-on-gke-using-envoy-proxy)
- [gRPC load balancing on Kubernetes with Linkerd](https://kubernetes.io/blog/2018/11/07/grpc-load-balancing-on-kubernetes-without-tears/#grpc-load-balancing-on-kubernetes-with-linkerd)

This scheme is different because this is `proxyless` where each gRPC client is in charge of where and how to distribute the load given signals from a central xDS server.

The backend system we will deploy here is `headless` which is rarely used so...this repo maybe of limited use.

>> *NOTE:* this is highly experimental and not supported by Google...i've only gotten it to work in a limited way (see appendix)

#### References

- [A27: xDS-Based Global Load Balancing](https://github.com/grpc/proposal/blob/master/A27-xds-global-load-balancing.md)
- [Kubernetes Headless Service](https://kubernetes.io/docs/concepts/services-networking/service/#headless-services)
- [GKE gRPC Loadbalancing](https://github.com/salrashid123/gcegrpc/tree/master/gke_svc_lb)
- [gRPC Load Balancing on Kubernetes without Tears](https://kubernetes.io/blog/2018/11/07/grpc-load-balancing-on-kubernetes-without-tears/)
- [LoadBalancing gRPC for Kubernetes Cluster Services](https://medium.com/google-cloud/loadbalancing-grpc-for-kubernetes-cluster-services-3ba9a8d8fc03)
- [Load Balancing gRPC services](https://www.evanjones.ca/grpc-load-balancing.html)

---

This repo describes running three services on a k8s cluster

* `docker.io/salrashid123/xds_lb_svc`
   1. xDS Server will use [DNS SRV](https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/#srv-records) to acquire all podIP addresses for a given service.
   2. For each POD ip, perform [gRPC Healthcheck Request](https://github.com/grpc/grpc/blob/master/doc/health-checking.md)
   3. For each service that is healthy, add that ip:port list as [envoy endpoints](https://www.envoyproxy.io/docs/envoy/latest/api-v2/api/v2/endpoint/endpoint_components.proto)
   4. Create envoy xDS Snapshot with the endpoints.
   5. loop to 1 every 10s

* `docker.io/salrashid123/be_xds_server`
  1. gRPC Backend Server that responds with the hostname 
  2. Backend Service is started in 'Headless' mode (meaning a kubernetes DNS SRV request will return _all_ pod `ip:ports` )

* `docker.io/salrashid123/fe_xds_server`
  1. Frontend HTTP server which when called will initialize a gRPC client with XDS balancer enabled.
  2. gRPC client will contact the xDS server and receive a list of valid gRPC server endpoints.
  3. gRPC client will connect directly to the backend services IP addresses and issue API call.
  4. Respond back to the http request with the list of responses from each backend.

(you are ofcourse to build and deploy your own servers)


---

Lets get started


```bash
minikube start        --driver=kvm2
```

(i'm using `kvm2`, you can omit that)

Apply the configuration.  

The initial set of backend pods started will be just `2`

```bash
$ kubectl apply -f .

$ kubectl get po,rc,deployment,svc
	NAME                                  READY   STATUS    RESTARTS   AGE
	pod/be1-deployment-6b7fb94bfb-hf6zv   1/1     Running   0          22s
	pod/be1-deployment-6b7fb94bfb-mkjqn   1/1     Running   0          22s
	pod/fe-deployment-787cd7ddd-8w4h5     1/1     Running   0          22s
	pod/xds-deployment-7585fbb89c-bv7vm   1/1     Running   0          21s

	NAME                             READY   UP-TO-DATE   AVAILABLE   AGE
	deployment.apps/be1-deployment   2/2     2            2           22s
	deployment.apps/fe-deployment    1/1     1            1           22s
	deployment.apps/xds-deployment   1/1     1            1           22s

	NAME                 TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)           AGE
	service/be1-srv      ClusterIP   None             <none>        50051/TCP         22s
	service/fe-srv       NodePort    10.109.170.230   <none>        8080:31022/TCP    22s
	service/kubernetes   ClusterIP   10.96.0.1        <none>        443/TCP           27h
	service/xds-srv      NodePort    10.102.173.147   <none>        18000:31826/TCP   22s
```

Note that the backend services are started as 'Headless services'

```yaml
apiVersion: v1
kind: Service
metadata:
  name: be1-srv
  labels:
    app: be1   
spec:
  clusterIP: None           <<<<< Headless
  ports:
  - name: grpc
    port: 50051   
  selector:
    app: be1
```

Now invoke the frontnd app.  

Remember the frontend app will inturn launch a gRPC client with one [conn](https://godoc.org/google.golang.org/grpc#ClientConn) object which will get connection info from the xDS server by itself.  Once the LB data is returned by the xDS server, the client will make 15 api calls


```golang
	address := fmt.Sprintf("xds:///" + svc)
	conn, err := grpc.Dial(address, grpc.WithInsecure())

	c := echo.NewEchoServerClient(conn)
	ctx := context.Background()
	var ret []string
	for i := 0; i < 15; i++ {
		r, err := c.SayHello(ctx, &echo.EchoRequest{Name: "unary RPC msg "})
		if err != nil {
			http.Error(w, fmt.Sprintf("Could not get RPC %v", err), http.StatusInternalServerError)
			return
		}
		log.Printf("RPC Response: %v %v", i, r)
		ret = append(ret, r.Message+"\n")
	}
```

What you will see in the output is the response showing the  host pod that handled each RPC (i.,e different pods)

```bash
$ curl -v `minikube  service fe-srv --url`/be1-srv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
```

Now increase the number of pods in the backend to 10

```bash
$ kubectl scale --replicas=10 deployment.apps/be1-deployment

$ kubectl get po,rc,deployment,svc
	NAME                                  READY   STATUS    RESTARTS   AGE
	pod/be1-deployment-6b7fb94bfb-2h899   1/1     Running   0          20s
	pod/be1-deployment-6b7fb94bfb-2ng5v   1/1     Running   0          20s
	pod/be1-deployment-6b7fb94bfb-8vnk4   1/1     Running   0          20s
	pod/be1-deployment-6b7fb94bfb-cdpct   1/1     Running   0          20s
	pod/be1-deployment-6b7fb94bfb-hf6zv   1/1     Running   0          111s
	pod/be1-deployment-6b7fb94bfb-jx6n8   1/1     Running   0          20s
	pod/be1-deployment-6b7fb94bfb-mkjqn   1/1     Running   0          111s
	pod/be1-deployment-6b7fb94bfb-s6d8j   1/1     Running   0          20s
	pod/be1-deployment-6b7fb94bfb-vbmvk   1/1     Running   0          20s
	pod/be1-deployment-6b7fb94bfb-xfhsc   1/1     Running   0          20s
	pod/fe-deployment-787cd7ddd-8w4h5     1/1     Running   0          111s
	pod/xds-deployment-7585fbb89c-bv7vm   1/1     Running   0          110s

	NAME                             READY   UP-TO-DATE   AVAILABLE   AGE
	deployment.apps/be1-deployment   10/10   10           10          111s
	deployment.apps/fe-deployment    1/1     1            1           111s
	deployment.apps/xds-deployment   1/1     1            1           111s

	NAME                 TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)           AGE
	service/be1-srv      ClusterIP   None             <none>        50051/TCP         111s
	service/fe-srv       NodePort    10.109.170.230   <none>        8080:31022/TCP    111s
	service/kubernetes   ClusterIP   10.96.0.1        <none>        443/TCP           27h
	service/xds-srv      NodePort    10.102.173.147   <none>        18000:31826/TCP   111s
```

Reissue the frontend query, you'll see the client automatically balanced requests to backends

```bash
$ curl -v `minikube  service fe-srv --url`/be1-srv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-vbmvk
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-2ng5v
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-s6d8j
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-8vnk4
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-s6d8j
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-2ng5v
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-mkjqn
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-jx6n8
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-2h899
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-cdpct
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-8vnk4
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-xfhsc
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-hf6zv
	Hello unary RPC msg   from hostname be1-deployment-6b7fb94bfb-s6d8j
```

---


### TODO:

Well, there are many todos here (i only did this sample on a cold sunday..)

#### Support multiple services

For one thing, i coudn't not get the XDS server handle multiple services.  That is, i'd like to deploy two different backends and then let xDS server query over both IP addresses.  The configmap i'd like to enable would be

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: xds-svc-config
data:
  svc_config.json: |
    {
        "services": [
          {
            "serviceName": "be1-srv",
            "namespace": "default",
            "portName": "grpc",
            "protocol": "tcp",
            "grpcServiceName": "echo.EchoServer",
            "zone": "us-central1-a",
            "region": "us-central1"
          },
          {
            "serviceName": "be2-srv",
            "namespace": "default",
            "portName": "grpc",
            "protocol": "tcp",
            "grpcServiceName": "echo.EchoServer",
            "zone": "us-central1-a",
            "region": "us-central1"
          }                                  
        ]
    }
```

However, if i invoke any backend service, the XDS server does not return the listener.  

> Perhaps explained in [Issue #349](https://github.com/envoyproxy/go-control-plane/issues/349)

```bash
$ curl -v `minikube  service fe-srv --url`/be1-srv
*   Trying 192.168.39.10:31085...
* Connected to 192.168.39.10 (192.168.39.10) port 31085 (#0)
> GET /be1-srv HTTP/1.1
> Host: 192.168.39.10:31085
> User-Agent: curl/7.72.0
> Accept: */*
> 
* Mark bundle as not supporting multiuse
< HTTP/1.1 500 Internal Server Error
< Content-Type: text/plain; charset=utf-8
< X-Content-Type-Options: nosniff
< Date: Sun, 17 Jan 2021 15:39:54 GMT
< Content-Length: 140
< 
Could not get RPC rpc error: code = Unavailable desc = name resolver error: xds: ListenerResource target be1-srv not found, watcher timeout

```

Here are the xDS Server logs...it seems to create a proper list of services/listeners but nothing is returned to the client

```log
$ kubectl logs pod/xds-deployment-7585fbb89c-d9lvr
time="2021-01-17T15:39:25Z" level=info msg="Starting control plane"
time="2021-01-17T15:39:25Z" level=info msg="management server listening" port=18000
time="2021-01-17T15:39:39Z" level=info msg="OnStreamOpen 1 open for Type []"
time="2021-01-17T15:39:39Z" level=info msg="OnStreamRequest 1  Request[type.googleapis.com/envoy.api.v2.Listener]"
time="2021-01-17T15:39:39Z" level=info msg="OnStreamRequest 1  Request[
	node:
	  {id:\"b7f9c818-fb46-43ca-8662-d3bdbcf7ec18~10.0.0.1\" 
	   metadata:
	     {fields:{
			   key:\"R_GCP_PROJECT_NUMBER\" 
			   value:{string_value:\"123456789012\"}}} 
			   locality:{zone:\"us-central1-a\"} 
			   build_version:\"gRPC Go 1.33.2\" 
			   user_agent_name:\"gRPC Go\" 
			   user_agent_version:\"1.33.2\" 
			   client_features:\"envoy.lb.does_not_support_overprovisioning\"
			   } 
		resource_names:\"be1-srv\"                                                               <<<<<<<<<<<<<
		type_url:\"type.googleapis.com/envoy.api.v2.Listener\"
	]"
time="2021-01-17T15:39:39Z" level=info msg="cb.Report()  callbacks" fetches=0 requests=1

time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating NodeID b7f9c818-fb46-43ca-8662-d3bdbcf7ec18~10.0.0.1"
time="2021-01-17T15:39:39Z" level=info msg="Successfully Opened svc_config.json"


time="2021-01-17T15:39:39Z" level=info msg=be1-srv
time="2021-01-17T15:39:39Z" level=info msg="SRV CNAME: _grpc._tcp.be1-srv.default.svc.cluster.local.\n"
time="2021-01-17T15:39:39Z" level=info msg="RPC HealthChekStatus: for 172-17-0-4.be1-srv.default.svc.cluster.local.:50051 SERVING"
time="2021-01-17T15:39:39Z" level=info msg="RPC HealthChekStatus: for 172-17-0-3.be1-srv.default.svc.cluster.local.:50051 SERVING"
time="2021-01-17T15:39:39Z" level=info msg="ClusterIPs: [172-17-0-4.be1-srv.default.svc.cluster.local.:50051 172-17-0-3.be1-srv.default.svc.cluster.local.:50051]"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating ENDPOINT for remoteHost:port 172-17-0-4.be1-srv.default.svc.cluster.local.:50051"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating ENDPOINT for remoteHost:port 172-17-0-3.be1-srv.default.svc.cluster.local.:50051"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating CLUSTER be1-srv-cluster"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating RDS be1-srv-vs"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating LISTENER be1-srv"


time="2021-01-17T15:39:39Z" level=info msg=be2-srv
time="2021-01-17T15:39:39Z" level=info msg="SRV CNAME: _grpc._tcp.be2-srv.default.svc.cluster.local.\n"
time="2021-01-17T15:39:39Z" level=info msg="RPC HealthChekStatus: for 172-17-0-7.be2-srv.default.svc.cluster.local.:50051 SERVING"
time="2021-01-17T15:39:39Z" level=info msg="RPC HealthChekStatus: for 172-17-0-8.be2-srv.default.svc.cluster.local.:50051 SERVING"
time="2021-01-17T15:39:39Z" level=info msg="ClusterIPs: [172-17-0-7.be2-srv.default.svc.cluster.local.:50051 172-17-0-8.be2-srv.default.svc.cluster.local.:50051]"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating ENDPOINT for remoteHost:port 172-17-0-7.be2-srv.default.svc.cluster.local.:50051"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating ENDPOINT for remoteHost:port 172-17-0-8.be2-srv.default.svc.cluster.local.:50051"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating CLUSTER be2-srv-cluster"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating RDS be2-srv-vs"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating LISTENER be2-srv"


time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>> creating snapshot Version 1"
time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>>    snapshot with Listener [
	 name:\"be1-srv\" api_listener:{api_listener:{[type.googleapis.com/envoy.config.filter.network.http_connection_manager.v2.HttpConnectionManager]:{rds:{config_source:{ads:{}} route_config_name:\"be1-srv-route\"}}}} 
	 name:\"be2-srv\" api_listener:{api_listener:{[type.googleapis.com/envoy.config.filter.network.http_connection_manager.v2.HttpConnectionManager]:{rds:{config_source:{ads:{}} route_config_name:\"be2-srv-route\"}}}}
	 ]"

time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>>    snapshot with EDS [
	cluster_name:\"be1-srv-cluster\" endpoints:{locality:{region:\"us-central1\" zone:\"us-central1-a\"} 
	lb_endpoints:{endpoint:{address:{socket_address:{address:\"172-17-0-4.be1-srv.default.svc.cluster.local.\" port_value:50051}}} health_status:HEALTHY} lb_endpoints:{endpoint:{address:{socket_address:{address:\"172-17-0-3.be1-srv.default.svc.cluster.local.\" port_value:50051}}} health_status:HEALTHY} load_balancing_weight:{value:1000}} 
	cluster_name:\"be2-srv-cluster\" endpoints:{locality:{region:\"us-central1\" zone:\"us-central1-a\"} 
	lb_endpoints:{endpoint:{address:{socket_address:{address:\"172-17-0-7.be2-srv.default.svc.cluster.local.\" port_value:50051}}} health_status:HEALTHY} lb_endpoints:{endpoint:{address:{socket_address:{address:\"172-17-0-8.be2-srv.default.svc.cluster.local.\" port_value:50051}}} health_status:HEALTHY} load_balancing_weight:{value:1000}}
	]"

time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>>    snapshot with CLS [
	name:\"be1-srv-cluster\" type:EDS eds_cluster_config:{eds_config:{ads:{}}} 
	name:\"be2-srv-cluster\" type:EDS eds_cluster_config:{eds_config:{ads:{}}}
	]"

time="2021-01-17T15:39:39Z" level=info msg=">>>>>>>>>>>>>>>>>>>    snapshot with RDS [
	name:\"be1-srv-route\" virtual_hosts:{name:\"be1-srv-vs\" domains:\"be1-srv\" routes:{match:{prefix:\"\"} route:{cluster:\"be1-srv-cluster\"}}} 
	name:\"be2-srv-route\" virtual_hosts:{name:\"be2-srv-vs\" domains:\"be2-srv\" routes:{match:{prefix:\"\"} route:{cluster:\"be2-srv-cluster\"}}}
	]"
```


#### Use k8s API

I used k8s DNS SRV requests to lookup the headless services target IPs.  You could also use the k8s api server to do the same but in this case, you would need API RBAC permissions.

```golang

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)
	k8sconfig, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal(err.Error())
	}

	clientSet, err = kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		log.Fatal(err.Error())
	}

		ctx := context.Background()
		services, err := clientSet.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
			//FieldSelector: "metadata.name=" + serviceName,
		})
		if err != nil {
			log.Printf("Get service from kubernetes cluster error:%v", err)
			return
		}

		for _, svc := range services.Items {
			log.Println("found Service %v", svc.Name)
		}
```