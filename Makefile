# Builds heyrobotsd with the companion app's web build inside it, so one
# binary serves both: open the server's address in a browser to use the app.

FLUTTER ?= flutter
WEB_DIST := service/internal/webapp/dist

.PHONY: build web run dev test clean

## build: the web app, then bin/heyrobotsd with it embedded
build: web
	cd service && go build -o ../bin/heyrobotsd ./cmd/heyrobotsd

## web: build the Flutter web app into the server's embed folder
web:
	cd app && $(FLUTTER) build web --release
	find $(WEB_DIST) -mindepth 1 ! -name README.md -exec rm -rf {} +
	cp -R app/build/web/. $(WEB_DIST)/

## run: build everything and start the server (app at http://127.0.0.1:7340)
run: build
	./bin/heyrobotsd

## dev: start the server serving app/build/web from disk, without re-embedding
dev:
	cd service && HEYROBOTS_WEB_DIR=$(CURDIR)/app/build/web go run ./cmd/heyrobotsd

## test: run the server's and the app's tests
test:
	cd service && go test ./...
	cd app && $(FLUTTER) test

## clean: remove the binary and the embedded web build
clean:
	rm -rf bin
	find $(WEB_DIST) -mindepth 1 ! -name README.md -exec rm -rf {} +
