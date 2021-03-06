package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/vmihailenco/redis/v2"
)

var config Config
var client *redis.Client

type Config struct {
	Port      string
	RedisURL  string
	Retention int64
}

type Check struct {
	Id  string `json:"id"`
	Url string `json:"url"`
}

type Measurement struct {
	Check             Check   `json:"check"`
	Id                string  `json:"id"`
	Location          string  `json:"location"`
	T                 int     `json:"t"`
	ExitStatus        int     `json:"exit_status"`
	HttpStatus        int     `json:"http_status,omitempty"`
	LocalIp           string  `json:"local_ip,omitempty"`
	PrimaryIp         string  `json:"primary_ip,omitempty"`
	NameLookupTime    float64 `json:"namelookup_time,omitempty"`
	ConnectTime       float64 `json:"connect_time,omitempty"`
	StartTransferTime float64 `json:"starttransfer_time,omitempty"`
	TotalTime         float64 `json:"total_time,omitempty"`
}

func (m *Measurement) record() {
	s, _ := json.Marshal(m)
	z := redis.Z{Score: float64(m.T), Member: string(s)}
	r := client.ZAdd(getRedisKey(m.Check.Id), z)
	if r.Err() != nil {
		log.Fatalf("Error while recording measuremnt %s: %v\n", m.Id, r.Err())
	}
}

func trimMeasurements(check_id string, seconds int64) {
	now := time.Now()
	epoch := now.Unix() - seconds
	r := client.ZRemRangeByScore(getRedisKey(check_id), "-inf", strconv.FormatInt(epoch, 10))
	if r.Err() != nil {
		log.Fatalf("Error while trimming check_id %s: %v\n", check_id, r.Err())
	}
}

func getRedisKey(check_id string) string {
	return "measurements:" + check_id
}

func getMeasurementsByRange(check_id string, r int64) []Measurement {
	now := time.Now()
	from := now.Unix() - r

	return getMeasurementsFrom(check_id, from)
}

func getMeasurementsFrom(check_id string, from int64) []Measurement {
	vals, err := client.ZRevRangeByScore(getRedisKey(check_id), redis.ZRangeByScore{
		Min: strconv.FormatInt(from, 10),
		Max: "+inf",
	}).Result()

	if err != nil {
		panic(err)
	}

	measurements := make([]Measurement, 0, 100)

	for _, v := range vals {
		var m Measurement
		json.Unmarshal([]byte(v), &m)
		measurements = append(measurements, m)
	}

	return measurements
}

func getFormValueWithDefault(req *http.Request, key string, def string) string {
	s := req.FormValue(key)
	if s != "" {
		return s
	} else {
		return def
	}
}

func getMeasurementsHandler(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	check_id := vars["check_id"]
	r_s := getFormValueWithDefault(req, "range", "10")

	r, err := strconv.ParseInt(r_s, 10, 64)
	if err != nil {
		panic(nil)
	}
	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(getMeasurementsByRange(check_id, r))
}

func postMeasurementsHandler(res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	measurements := make([]Measurement, 0, 100)

	err := decoder.Decode(&measurements)
	if err != nil {
		panic(err)
	}

	for _, m := range measurements {
		m.record()
		trimMeasurements(m.Check.Id, config.Retention)
	}

	log.Printf("fn=post_measurements count=%d\n", len(measurements))
}

func connectToRedis(config Config) {
	u, err := url.Parse(config.RedisURL)
	if err != nil {
		panic(err)
	}

	client = redis.NewTCPClient(&redis.Options{
		Addr:     u.Host,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
}

func main() {
	config = Config{}
	flag.StringVar(&config.Port, "port", "5000", "port the HTTP server should bind to")
	flag.StringVar(&config.RedisURL, "redis_url", "redis://localhost:6379", "redis url")
	flag.Int64Var(&config.Retention, "retention", 60, "second of each measurement to keep")
	flag.Parse()

	connectToRedis(config)

	r := mux.NewRouter()

	r.HandleFunc("/checks/{check_id}/measurements", getMeasurementsHandler).Methods("GET")
	r.HandleFunc("/measurements", postMeasurementsHandler).Methods("POST")
	http.Handle("/", r)

	log.Printf("fn=main listening=true port=%s\n", config.Port)

	err := http.ListenAndServe(":"+config.Port, nil)
	if err != nil {
		panic(err)
	}
}
