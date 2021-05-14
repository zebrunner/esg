FROM golang:1.14-alpine

RUN apk add -U ca-certificates tzdata mailcap curl && rm -Rf /var/cache/apk/*

# Install migrate tool
RUN curl -L https://github.com/golang-migrate/migrate/releases/download/v4.14.1/migrate.linux-amd64.tar.gz | tar -xvz
RUN cp migrate.linux-amd64 /usr/bin/migrate
RUN echo $PATH

# Build esg
COPY ./ /esg
WORKDIR /esg
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build
