.PHONY: dependencies run build push
tag = 0.0.10

dependencies:
	docker-compose up -d postgres

up: dependencies
	docker-compose up govivint

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
