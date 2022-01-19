#!/bin/bash

go build cmd/data-importer/data-importer.go 

for FILE in ../datasets/bodds_archive_20220107/*
  do 
    ./data-importer file transxchange $FILE | tee test/logs/$(basename ${FILE}.log)
  done

rm ./data-importer