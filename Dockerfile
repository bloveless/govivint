FROM golang:1.17.3-bullseye as builder

RUN useradd -ms /bin/bash golang

USER golang
RUN mkdir /home/golang/app
WORKDIR /home/golang/app

