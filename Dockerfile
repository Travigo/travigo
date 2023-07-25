FROM ubuntu:22.04

RUN apt-get update && apt-get install ca-certificates -y && update-ca-certificates

WORKDIR /

COPY ./travigo /travigo
COPY ./transforms /transforms

RUN chmod +x /travigo

RUN useradd travigo
USER travigo

ENTRYPOINT ["/travigo"]
