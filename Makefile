.PHONY: dependencies run build push
tag = 0.0.18

ifneq (,$(wildcard ./.env.local))
    include .env.local
    export
endif

dependencies:
	docker-compose up -d postgres

up: dependencies
	docker-compose up govivint

# In order to run locally you'll need to create a .env.local file which has everything from .env
# and the docker-compose environment for the govivint service including any changes necessary to
# run locally I.E. the POSTGRES_HOST is "localhost" and not "postgres" when running locally.
run-local:
	go run main.go

down:
	docker-compose down

clean:
	docker-compose down -v

build:
	docker build . --tag bloveless/govivint:$(tag)

push: build
	docker push bloveless/govivint:$(tag)

deploy: push
	kubectl apply -f k8s

