FROM ubuntu:22.04

RUN apt-get update && apt-get install ca-certificates -y && update-ca-certificates

WORKDIR /

COPY ./travigo /travigo
COPY ./data /data

RUN chmod +x /travigo

RUN useradd travigo
USER travigo

ENTRYPOINT ["/travigo"]
