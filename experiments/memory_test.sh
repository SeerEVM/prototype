#!/bin/bash

go run ../main.go --indicator 6 --blockNum 1000

sleep 10

go run ../main.go --indicator 7 --blockNum 1000 --perceptron=false --fast=false --checkpoint=false

sleep 10

go run ../main.go --indicator 7 --blockNum 1000 --perceptron=true --fast=false --checkpoint=false

sleep 10

go run ../main.go --indicator 7 --blockNum 1000 --perceptron=true --fast=true --checkpoint=true
