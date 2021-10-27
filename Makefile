build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o manager .

build-image:
	docker build -t liuliluo/limiter:v0.4 .

push:
	docker push liuliluo/limiter:v0.4