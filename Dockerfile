FROM golang:latest 

LABEL version="1.0"

RUN mkdir /go/src/app

RUN go get -u github.com/thomaslamendola/loggor
RUN go get -u go.mongodb.org/mongo-driver/bson
RUN go get -u go.mongodb.org/mongo-driver/mongo

ADD ./main.go /go/src/app
COPY ./config.json /go/src/app

WORKDIR /go/src/app 

# RUN go test -v 
RUN go build

CMD ["./app"]
EXPOSE 8787