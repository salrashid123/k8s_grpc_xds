module main

go 1.15

require (
	github.com/salrashid123/grpc_health_proxy/example/src/echo v0.0.0
	github.com/gorilla/mux v1.8.0 // indirect
	golang.org/x/net v0.0.0-20201110031124-69a78807bb2b
	google.golang.org/grpc  v1.38.0
)

replace github.com/salrashid123/grpc_health_proxy/example/src/echo => ./echo

