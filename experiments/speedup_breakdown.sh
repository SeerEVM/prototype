#!/bin/bash

go run ../main.go --indicator 2 --ratio 0.1 --repair=false --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.1 --repair=true  --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.1 --repair=true  --perceptron=true  --fast=false
go run ../main.go --indicator 2 --ratio 0.1 --repair=true  --perceptron=true  --fast=true

go run ../main.go --indicator 2 --ratio 0.2 --repair=false --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.2 --repair=true  --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.2 --repair=true  --perceptron=true  --fast=false
go run ../main.go --indicator 2 --ratio 0.2 --repair=true  --perceptron=true  --fast=true

go run ../main.go --indicator 2 --ratio 0.3 --repair=false --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.3 --repair=true  --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.3 --repair=true  --perceptron=true  --fast=false
go run ../main.go --indicator 2 --ratio 0.3 --repair=true  --perceptron=true  --fast=true

go run ../main.go --indicator 2 --ratio 0.4 --repair=false --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.4 --repair=true  --perceptron=false --fast=false
go run ../main.go --indicator 2 --ratio 0.4 --repair=true  --perceptron=true  --fast=false
go run ../main.go --indicator 2 --ratio 0.4 --repair=true  --perceptron=true  --fast=true
