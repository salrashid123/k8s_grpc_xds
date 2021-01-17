FROM golang:1.15 as build

ENV GO111MODULE=on

WORKDIR /app

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build xds_server.go

FROM gcr.io/distroless/base
COPY --from=build /app/xds_server /

EXPOSE 18000

ENTRYPOINT ["/xds_server", "--port=18000", "--debug"]