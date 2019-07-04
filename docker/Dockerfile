FROM golang:1.12 as builder

COPY . /go/src/github.com/patoarvizu/cloudflare-route53-controller/

WORKDIR /go/src/github.com/patoarvizu/cloudflare-route53-controller/

RUN go get -u github.com/golang/dep/cmd/dep

RUN dep ensure -vendor-only

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /go/src/github.com/patoarvizu/cloudflare-route53-controller/bin/cloudflare-route53-controller /go/src/github.com/patoarvizu/cloudflare-route53-controller

FROM alpine:3.9

RUN apk update && apk add ca-certificates

COPY --from=builder /go/src/github.com/patoarvizu/cloudflare-route53-controller/bin/cloudflare-route53-controller /

WORKDIR /

CMD /cloudflare-route53-controller