module main

go 1.15

require (
	echo v0.0.0
	github.com/gorilla/mux v1.8.0 // indirect
	golang.org/x/net v0.0.0-20201110031124-69a78807bb2b
	google.golang.org/grpc v1.33.2
)

replace echo => ./echo
