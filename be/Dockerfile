FROM golang:1.15 as build

RUN apt-get update -y && apt-get install -y build-essential wget unzip curl


RUN curl -OL https://github.com/google/protobuf/releases/download/v3.2.0/protoc-3.2.0-linux-x86_64.zip && \
    unzip protoc-3.2.0-linux-x86_64.zip -d protoc3 && \
    mv protoc3/bin/* /usr/local/bin/ && \
    mv protoc3/include/* /usr/local/include/


ENV GO111MODULE=on
RUN go get -u github.com/golang/protobuf/protoc-gen-go   

WORKDIR /app

COPY . .


RUN go mod download


RUN /usr/local/bin/protoc -I . --include_imports --include_source_info --descriptor_set_out=echo/echo.proto.pb  --go_out=plugins=grpc:/ echo/echo.proto

RUN export GOBIN=/app/bin && go install grpc_server.go

FROM gcr.io/distroless/base

COPY --from=build /app/bin /

EXPOSE 50051

ENTRYPOINT ["/grpc_server"]
