FROM golang:1.25-bookworm

WORKDIR /app

COPY . .

ENV GOPROXY=https://proxy.golang.org,direct

RUN if [ -d /app/forensic-operator ]; then cd /app/forensic-operator; fi; \
	for i in 1 2 3; do go mod download && break || sleep 3; done; \
	go build -mod=mod -o /controller ./cmd/controller

ENTRYPOINT ["/controller"]