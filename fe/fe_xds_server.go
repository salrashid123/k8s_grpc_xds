package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/salrashid123/grpc_health_proxy/example/src/echo"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/xds"
)

const ()

var (
	httpport = flag.String("httpport", ":8080", "httpport")
)

type server struct{}

func fronthandler(w http.ResponseWriter, r *http.Request) {
	log.Println("/ called")

	svc := mux.Vars(r)["svc"]
	if svc == "" {
		svc = "be1-srv"
	}

	log.Println("Looking up service %s", svc)

	address := fmt.Sprintf("xds:///" + svc)
	conn, err := grpc.Dial(address, grpc.WithInsecure())

	if err != nil {
		http.Error(w, fmt.Sprintf("Could not connect %v", err), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

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

	fmt.Fprint(w, fmt.Sprintf("%v", ret))
}

func main() {
	flag.Parse()

	router := mux.NewRouter()
	router.Methods(http.MethodGet).Path("/{svc}").HandlerFunc(fronthandler)

	var server *http.Server
	server = &http.Server{
		Addr:    *httpport,
		Handler: router,
	}
	http2.ConfigureServer(server, &http2.Server{})
	fmt.Println("Starting Server..")
	err := server.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}
