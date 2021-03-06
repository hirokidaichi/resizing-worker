package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/crowdmob/goamz/aws"
	"github.com/crowdmob/goamz/s3"
	"github.com/crowdmob/goamz/sqs"
	"github.com/nfnt/resize"
	"image"
	"image/jpeg"
	_ "image/png"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"
)

var AUTH aws.Auth
var REGION aws.Region
var S3CLIENT *s3.S3
var SQSCLIENT *sqs.SQS

const MAX_DEQ_COUNT = 10
const HIDDEN_SEC = 20

type Setting struct {
	AccessKey string   `json:"aws.key"`
	SecretKey string   `json:"aws.secret"`
	Region    string   `json:"aws.region"`
	Queues    []string `json:"sqs.queues"`
	Polling   string   `json:"sqs.polling"`
	Workers   int      `json:"workers"`
	Port      int      `json:"port"`
}

/*
$ export AWS_ACCESS_KEY_ID=<access_key>
$ export AWS_SECRET_ACCESS_KEY=<secret_key>
*/
func (self *Setting) GetAuth() aws.Auth {
	if self.AccessKey == "" {
		self.AccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if self.SecretKey == "" {
		self.SecretKey = os.Getenv("AWS_SECRET_KEY")
	}
	if self.AccessKey == "" || self.SecretKey == "" {
		log.Fatal("cannot find aws auth. please set in setting.json or in env")
	}
	return aws.Auth{
		AccessKey: self.AccessKey,
		SecretKey: self.SecretKey,
	}
}
func (self *Setting) GetRegion() aws.Region {
	if self.Region == "" {
		self.Region = os.Getenv("AWS_REGION")
	}
	if self.Region == "" {
		log.Fatal("cannot find aws region. please set in setting.json or in env")
	}
	return aws.GetRegion(self.Region)
}

func (self *Setting) GetPollingTime() time.Duration {
	d, err := time.ParseDuration(self.Polling)
	if err != nil {
		log.Fatal(err)
	}
	return d
}

func main() {
	var setting Setting
	file, err := ioutil.ReadFile("./setting.json")
	if err != nil {
		log.Println("./setting.json : not exists ")
	} else {
		json.Unmarshal(file, &setting)
	}
	flag.Parse()
	AUTH = setting.GetAuth()
	REGION = setting.GetRegion()
	S3CLIENT = s3.New(AUTH, REGION)
	SQSCLIENT = sqs.New(AUTH, REGION)

	if flag.Arg(0) == "httpserver" {
		if setting.Port == 0 {
			setting.Port = 8080
		}
		log.Println("as httpserver")
		http.HandleFunc("/", HandleMessage)
		http.ListenAndServe(fmt.Sprintf(":%d", setting.Port), nil)
		return
	}

	if flag.Arg(0) == "watcher" {
		log.Println("as watcher..")
		c := Collector(setting.Queues, setting.GetPollingTime())
		dispatcher := NewDispatcher(setting.Workers)
		dispatcher.Start()
		idx := 0
		for v := range c {
			idx = dispatcher.Do(v, idx)
		}
		dispatcher.Stop()
		log.Println("terminated")
		return
	}
	fmt.Println(`
usage: resizing-worker <command>

the commands are:

    httpserver  Work as HTTP server and receive JSON messages and process it
    watcher     Watch SQS queues and retrieve messages and process it
    `)
}

func HandleMessage(w http.ResponseWriter, r *http.Request) {
	messageText, err := ReadBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	message, err := ParseMessage(messageText)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err = message.Handle(0); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func ReadBody(r *http.Request) (string, error) {
	content, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return "", err
	}
	return string(content), nil
}

type Dispatcher []*Worker

func NewDispatcher(n int) Dispatcher {
	d := make([]*Worker, n)
	for i := 0; i < n; i++ {
		d[i] = NewWorker(i)
	}
	return d
}

func (d Dispatcher) Start() {
	for _, v := range d {
		v.Start()
	}
}

func (d Dispatcher) Do(t Task, n int) int {
	d[n].Do(t)
	return (n + 1) % len(d)
}

func (d Dispatcher) Stop() {
	wg := new(sync.WaitGroup)
	for _, v := range d {
		wg.Add(1)
		v.Stop(wg)
	}
	wg.Wait()
}

type Worker struct {
	stopChan chan bool
	quitChan chan bool
	workChan chan Task
	ID       int
}

func NewWorker(n int) *Worker {
	self := &Worker{
		stopChan: make(chan bool),
		quitChan: make(chan bool),
		workChan: make(chan Task, 100),
		ID:       n,
	}
	return self
}

func (self Worker) Do(t Task) {
	self.workChan <- t
}

func (self Worker) Exec(t Task) error {
	q := t.Queue
	m := t.Message
	defer func() {
		_, err := q.DeleteMessage(m)
		log.Printf("[log] deleted %s", m.MessageId)
		if err != nil {
			log.Println(err)
		}
	}()
	message, err := ParseMessage(t.Message.Body)
	if err != nil {
		return err
	}
	return message.Handle(self.ID)

}

func (self Worker) Start() {
	log.Printf("worker[%d] started", self.ID)
	go func() {
		defer func() {
			log.Printf("worker[%d] finished", self.ID)
			self.quitChan <- true
		}()
		for {
			select {
			case t := <-self.workChan:
				err := self.Exec(t)
				if err != nil {
					log.Println(err)
				}
			case <-self.stopChan:
				return
			}
		}
	}()

}
func (self Worker) Stop(ws *sync.WaitGroup) {
	go func() {
		defer ws.Done()
		self.stopChan <- true
		<-self.quitChan
	}()
}

type S3File struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
}

func (self *S3File) Get() (bytes []byte, err error) {
	bucket := S3CLIENT.Bucket(self.Bucket)
	bytes, err = bucket.Get(self.Key)
	return
}

func (self *S3File) Put(bytes []byte, contentType string) error {
	bucket := S3CLIENT.Bucket(self.Bucket)
	err := bucket.Put(self.Key, bytes, contentType, "", s3.Options{})
	return err
}

type Message struct {
	From   *S3File `json:"from"`
	To     *S3File `json:"to"`
	Method string  `json:"method"`
	Width  uint    `json:"width"`
	Height uint    `json:"height"`
}

func (self *Message) Handle(id int) error {
	defer func() {
		log.Printf("[log] %d %s/%s -> %s/%s (%dx%d) %s",
			id,
			self.From.Bucket,
			self.From.Key,
			self.To.Bucket,
			self.To.Key,
			self.Width,
			self.Height,
			self.Method,
		)
	}()
	fromBytes, err := self.From.Get()
	if err != nil {
		return err
	}
	toBytes, err := self.Resize(fromBytes)
	if err != nil {
		return err
	}
	return self.To.Put(toBytes, "image/json")
}

/*
SEE ALSO : https://github.com/nfnt/resize
*/
func (self *Message) GetMethod() resize.InterpolationFunction {
	switch self.Method {
	case "NearestNeighbor":
		return resize.NearestNeighbor
	case "Bilinear":
		return resize.Bilinear
	case "Bicubic":
		return resize.Bicubic
	case "MitchellNetravali":
		return resize.MitchellNetravali
	case "Lanczos2":
		return resize.Lanczos2
	case "Lanczos3":
		return resize.Lanczos3
	default:
		return resize.Lanczos3
	}
}

func (self *Message) Resize(imageBytes []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		println("here")
		return nil, err
	}
	m := resize.Resize(self.Width, self.Height, img, self.GetMethod())
	writer := bytes.NewBuffer([]byte(""))
	err = jpeg.Encode(writer, m, nil)
	if err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func ParseMessage(jsonText string) (*Message, error) {
	self := &Message{}
	err := json.Unmarshal([]byte(jsonText), self)
	if err != nil {
		return nil, err
	}
	return self, nil
}

type Task struct {
	Message *sqs.Message
	Queue   *sqs.Queue
}

func Collector(names []string, d time.Duration) chan Task {
	sqsClient := sqs.New(AUTH, REGION)
	queues := make([]*sqs.Queue, len(names))
	for i, v := range names {
		q, err := sqsClient.GetQueue(v)
		if err != nil {
			log.Fatal(err)
		}
		queues[i] = q
	}
	res := make(chan Task)
	timer := time.Tick(1 * time.Second)
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, os.Interrupt)
	go func() {
		defer close(res)
		for {
			select {
			case <-timer:
				for _, q := range queues {
					messages, err := q.ReceiveMessageWithVisibilityTimeout(MAX_DEQ_COUNT, HIDDEN_SEC)
					if err != nil {
						log.Println(err)
					}
					if d := len(messages.Messages); d != 0 {
						log.Printf("[info] Getting %d messages", d)
					}
					for _, m := range messages.Messages {
						// copy struct
						d := m
						res <- Task{
							Message: &d,
							Queue:   q,
						}
					}
				}
			case <-sigterm:
				log.Println("[info] finishing workers..")
				return
			}
		}
	}()
	return res
}
