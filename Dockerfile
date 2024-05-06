# go + linux for building
FROM golang:1.17-alpine AS build

# proj root
WORKDIR /build

# get dependancies
COPY go.mod go.sum ./
RUN go mod download

# get rest of files 
COPY . .
RUN go build -o loadtest .

# start a new image, alpine like the buikd one
FROM alpine:latest

# new proj root
WORKDIR /root/

# copy the exe and other files over
COPY --from=build /build/loadtest .

# run the application
CMD ["./loadtest"]