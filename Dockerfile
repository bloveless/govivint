FROM golang:1.17.3-bullseye as base

RUN useradd -ms /bin/bash golang

USER golang
RUN mkdir /home/golang/app
WORKDIR /home/golang/app

# -----------------------------------------------

FROM base as builder

COPY . .
RUN go build -o govivint .

# -----------------------------------------------

FROM debian:bullseye-slim

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

RUN useradd -ms /bin/bash golang

USER golang
RUN mkdir /home/golang/app
WORKDIR /home/golang/app

COPY --from=builder --chown=golang:golang /home/golang/app/govivint /home/golang/app/govivint

CMD ["/home/golang/app/govivint"]

