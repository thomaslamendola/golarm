FROM golang:latest AS builder

WORKDIR /go/src/github.com/thomaslamendola/golarm/

RUN go get -d -v github.com/thomaslamendola/loggor
RUN go get -d -v go.mongodb.org/mongo-driver/bson
RUN go get -d -v go.mongodb.org/mongo-driver/mongo

COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app .
COPY config.json .

FROM alpine:latest  
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /go/src/github.com/thomaslamendola/golarm/app .
COPY --from=builder /go/src/github.com/thomaslamendola/golarm/config.json .
CMD ["./app"] 

EXPOSE 8787