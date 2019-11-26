package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/thomaslamendola/loggor"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var _collection *mongo.Collection
var _hostName string
var _config configuration

var _m map[string]*time.Timer

type echo struct {
	ID                   string        `json:"id"`
	Event                string        `json:"event"`
	URI                  string        `json:"uri"`
	HostName             string        `json:"hostname"`
	StartedAt            time.Time     `json:"startedat"`
	Timeout              int           `json:"timeout"`
	Duration             time.Duration `json:"duration"`
	CalculatedExpiryTime time.Time     `json:"calculatedexpirytime"`
	IgnoreMissed         bool          `json:"ignoremissed"`
}

type configuration struct {
	MongoConnectionString string
	DatabaseName          string
	CollectionName        string
	ExpireAfterSeconds    int32
	LogName               string
	LogBasePath           string
}

func process(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		loggor.Error("Processing request: 404 Not Found - " + r.RequestURI)
		http.Error(w, "", http.StatusNotFound)
		return
	}
	switch r.Method {
	case "POST":
		w.Header().Set("Content-Type", "application/json")
		var e echo
		if r.Body == nil {
			loggor.Error("Processing request: Body not set")
			http.Error(w, "Please set the request body", 400)
			return
		}
		e.HostName = _hostName
		err := json.NewDecoder(r.Body).Decode(&e)
		if err != nil {
			loggor.Error("Processing request: body not matching supported model")
			http.Error(w, err.Error(), 400)
			return
		}
		if e.ID == "" || e.Event == "" || e.Timeout == 0 || e.URI == "" {
			loggor.Error("Processing request: ID, Event, Timeout and URI are compulsory fields")
			http.Error(w, "ID, Event, Timeout and URI are compulsory fields", 400)
			return
		}

		e.StartedAt = time.Now().UTC()
		e.Duration = time.Second * time.Duration(e.Timeout)
		e.CalculatedExpiryTime = e.StartedAt.Add(e.Duration)

		_, err = _collection.InsertOne(context.Background(), e)
		if err != nil {
			loggor.Error("Adding Mongo entry: an error occurred while inserting the document - " + err.Error())
			http.Error(w, err.Error(), 400)
			return
		}
		loggor.Info("Adding Mongo entry: Done")
		loggor.Info("Scheduling callback")
		go schedule(e)
		json.NewEncoder(w).Encode(e)
		loggor.Info("Scheduling callback: " + e.ID + " scheduled in " + strconv.Itoa(e.Timeout))
	case "DELETE":
		w.Header().Set("Content-Type", "application/json")
		var e echo
		if r.Body == nil {
			loggor.Error("Cancelling request: Body not set")
			http.Error(w, "Please send a request body", 400)
			return
		}
		err := json.NewDecoder(r.Body).Decode(&e)
		if err != nil {
			loggor.Error("Cancelling request: body not matching supported model")
			http.Error(w, err.Error(), 400)
			return
		}
		if e.ID == "" {
			loggor.Error("Cancelling request: ID is a compulsory fields")
			http.Error(w, "ID is a compulsory fields", 400)
			return
		}
		existingTimer, found := _m[e.ID]
		loggor.Info("Cancelling event " + e.ID)

		if !found {
			loggor.Error("Cancelling event: Schedule not found for the ID specified")
			http.Error(w, "Schedule not found for the ID specified", 400)
			return
		}

		stop := existingTimer.Stop()
		if stop {
			loggor.Info("Cancelling event: Callback " + e.ID + " cancelled")
			delete(_m, e.ID)
		} else {
			loggor.Error("Cancelling event: something went wrong when attmpting to stop scheduled callback")
			http.Error(w, "Something went wrong when attmpting to stop scheduled callback", 400)
			return
		}
		filter := bson.M{"id": e.ID}
		res, err := _collection.DeleteOne(context.Background(), filter)
		if err != nil {
			loggor.Error("Cancelling event: failed to delete entry from DB for " + e.ID + " - " + err.Error())
			http.Error(w, err.Error(), 400)
			return
		}
		count := res.DeletedCount
		loggor.Info("Cancelling event: documents deleted from Mongo: " + strconv.Itoa(int(count)))
		json.NewEncoder(w).Encode(e)

	default:
		loggor.Error("Request received: Method not allowed")
		http.Error(w, "", 405)
	}
}

func schedule(e echo) {
	timer := time.NewTimer(e.Duration)
	_m[e.ID] = timer
	<-timer.C
	go execCallback(e)
}

func execCallback(e echo) {
	loggor.Info("Executing callback: " + e.ID)

	filter := bson.M{"id": e.ID}
	res, err := _collection.DeleteOne(context.Background(), filter)
	if err != nil {
		loggor.Error("Executing callback: cannot delete entry from Mongo - " + err.Error())
		return
	}
	count := res.DeletedCount
	loggor.Info("Executing callback: documents deleted from Mongo: " + strconv.Itoa(int(count)))

	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(e)
	resp, err := http.Post(e.URI, "application/json; charset=utf-8", b)
	if err != nil {
		loggor.Error("Executing callback: cannot POST against previously set callback uri: " + err.Error())
		return
	}
	defer resp.Body.Close()
	loggor.Info("Executing callback: " + e.ID + " processed - " + resp.Status + " - time taken: " + strconv.Itoa(int(time.Now().UTC().Sub(e.StartedAt)/time.Second)))
}

func loadConfiguration() configuration {
	var config configuration
	configFile, err := os.Open("config.json")
	defer configFile.Close()
	if err != nil {
		fmt.Println(err.Error())
	}
	jsonParser := json.NewDecoder(configFile)
	jsonParser.Decode(&config)
	return config
}

func main() {

	port := flag.String("port", "8787", "Port number for the HTTP server")
	flag.Parse()
	fmt.Println("Starting up and loading config...")
	_config = loadConfiguration()
	fmt.Println("Done... please check the log file for additional information")

	var errHostName error
	_hostName, errHostName = os.Hostname()
	if errHostName != nil {
		_hostName = "Default"
	}

	loggor.Initialize(_config.LogBasePath, _config.LogName, "Golarm", _hostName)
	loggor.Info("Application starting...")

	_m = make(map[string]*time.Timer)

	setupAndCheckStorage()

	http.HandleFunc("/", process)
	loggor.Info("Starting Golarm Butler... (listening on port " + *port + ")")

	var gracefulStop = make(chan os.Signal)
	signal.Notify(gracefulStop, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-gracefulStop
		loggor.Info("Terminating app, closing gracefully...")
		time.Sleep(1 * time.Second)
		//notifying something?
		os.Exit(0)
	}()
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}

func setupAndCheckStorage() {
	client, err := mongo.NewClient(options.Client().ApplyURI(_config.MongoConnectionString))
	if err != nil {
		loggor.Error("Setting up Mongo connection: cannot create a client - " + err.Error())
		return
	}
	err = client.Connect(context.TODO())
	if err != nil {
		loggor.Error("Setting up Mongo connection: cannot connect to the DB - " + err.Error())
		return
	}
	_collection = client.Database(_config.DatabaseName).Collection(_config.CollectionName)
	loggor.Info("Setting up Mongo connection: dropping indexes")
	indexView := _collection.Indexes()
	_, err = indexView.DropAll(context.Background())
	if err != nil {
		loggor.Error("Setting up Mongo connection: cannot drop all indexes - " + err.Error())
		return
	}
	loggor.Info("Setting up Mongo connection: creating calculatedexpirytime ttl index")

	_, err = indexView.CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.M{"calculatedexpirytime": 1},
		Options: &options.IndexOptions{ExpireAfterSeconds: &_config.ExpireAfterSeconds},
	})
	if err != nil {
		loggor.Error("Setting up Mongo connection: cannot create calculatedexpirytime ttl index")
		return
	}
	loggor.Info("Setting up Mongo connection: creating id unique index")

	unique := true
	_, err = indexView.CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.M{"id": 1},
		Options: &options.IndexOptions{Unique: &unique},
	})
	if err != nil {
		loggor.Error("Setting up Mongo connection: cannot create id unique index")
		return
	}
	loggor.Info("Setting up Mongo connection: Done")

	filter := bson.M{"hostname": _hostName}

	loggor.Info("Recovering unprocessed callback schedules: starting")
	cur, err := _collection.Find(context.Background(), filter)
	if err != nil {
		loggor.Error("Recovering unprocessed callback schedules: cannot query collection - " + err.Error())
		return
	}
	defer cur.Close(context.Background())
	for cur.Next(context.Background()) {
		var e echo
		err := cur.Decode(&e)
		if err != nil {
			loggor.Error("Recovering unprocessed callback schedules: failed to decode object from db - " + err.Error())
			return
		}
		if e.CalculatedExpiryTime.Unix() < time.Now().UTC().Unix() && e.IgnoreMissed {
			loggor.Info("Recovering unprocessed callback schedules: skipping past scheduled callbacks to be ignored")
			continue
		}
		loggor.Info("Recovering unprocessed callback schedules: rescheduling " + e.ID)
		e.Duration = -(time.Now().UTC().Sub(e.CalculatedExpiryTime))
		loggor.Info("Recovering unprocessed callback schedules: new duration: " + strconv.Itoa(int(e.Duration.Seconds())))
		go schedule(e)
	}
	if err := cur.Err(); err != nil {
		loggor.Info("Recovering unprocessed callback schedules: Mongo cursor error - " + err.Error())
		return
	}
}
