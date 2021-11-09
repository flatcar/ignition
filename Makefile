.PHONY: all
all:
	./build

.PHONY: vendor
vendor:
	go mod vendor
