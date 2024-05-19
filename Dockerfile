# syntax=docker/dockerfile:1

# set image
FROM golang:latest

# set destination 
WORKDIR /app

# copy project contents
COPY . .

# install go dependencies
RUN go mod download

# build
RUN CGO_ENABLED=0 GOOS=linux go build -o /converter .

# going public
EXPOSE ${WEB_PORT_PUBLISH}

# exec
CMD ["/converter"]