FROM golang:1.25-bookworm

WORKDIR /app

COPY . .

RUN if [ -d /app/forensic-operator ]; then cd /app/forensic-operator; fi; \
	go build -o /collector ./cmd/collector

ENTRYPOINT ["/collector"]
