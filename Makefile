.PHONY: test smoke

test:
	go test ./...

smoke:
	go test ./smoke/ -tags=smoke -v -timeout 30m -count=1
