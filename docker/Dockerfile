FROM golang:1.12 as builder

COPY . /go/src/github.com/patoarvizu/cloudflare-route53-controller/

WORKDIR /go/src/github.com/patoarvizu/cloudflare-route53-controller/

RUN go get -u github.com/golang/dep/cmd/dep

RUN dep ensure -vendor-only

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /go/src/github.com/patoarvizu/cloudflare-route53-controller/bin/cloudflare-route53-controller /go/src/github.com/patoarvizu/cloudflare-route53-controller

FROM alpine:3.9

ARG GIT_COMMIT="unspecified"
LABEL GIT_COMMIT=$GIT_COMMIT

ARG GIT_TAG=""
LABEL GIT_TAG=$GIT_TAG

ARG COMMIT_TIMESTAMP="unspecified"
LABEL COMMIT_TIMESTAMP=$COMMIT_TIMESTAMP

ARG AUTHOR_EMAIL="unspecified"
LABEL AUTHOR_EMAIL=$AUTHOR_EMAIL

ARG SIGNATURE_KEY="undefined"
LABEL SIGNATURE_KEY=$SIGNATURE_KEY

RUN apk update && apk add ca-certificates

COPY --from=builder /go/src/github.com/patoarvizu/cloudflare-route53-controller/bin/cloudflare-route53-controller /

WORKDIR /

CMD /cloudflare-route53-controller