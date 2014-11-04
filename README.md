
# resizing-worker 
an implementation of jpeg resize worker for sqs and s3

# how to install

```
go get github.com/hirokidaichi/resizing-worker
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
