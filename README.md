
# resizing-worker 
an implementation of jpeg resize worker for sqs and s3

# how to install

```
go get github.com/hirokidaichi/resizing-worker
```

# usage

```
usage: resizing-worker <command>

the commands are:

    httpserver  Work as HTTP server and receive JSON messages and process it
    watcher     Watch SQS queues and retrieve messages and process it
```   

# use as a container

```
docker pull hirokidaichi/resizing-worker
```

```
docker run -t ¥
    -e "AWS_ACCESS_KEY_ID=YOUR_ACEESS_KEY" ¥
    -e "AWS_SECRET_ACCESS_KEY=YOUR_SECRET_KEY" ¥
    -e "AWS_REGION=REGION" ¥
    hirokidaichi/resizing-worker
```

# setting.json

```
{
    "aws.key": "YOUR KEY",
    "aws.secret": "YOUR SECRET",
    "aws.region": "ap-northeast-1",
    "sqs.queues": ["thumbnail"],
    "sqs.polling": "5s",
    "workers": 10
}
```

```
{
    "aws.key": "YOUR KEY",
    "aws.secret": "YOUR SECRET",
    "aws.region": "ap-northeast-1",
    "port" : 8080
}

```

# sqs message format

```

{
    "from" : {
        "bucket" : "bucket-name",
         "key" : "image.name.jpg"
    },
    "to" : {
        "bucket" : "bucket-name",
        "key" : "image.name.128x128.jpg"
    },
    "method" : "Bicubic",
    "width" : 128,
    "height" : 128
}
```
