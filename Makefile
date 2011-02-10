

all:
	8g cw-decode.go
	8l -o cw-decode cw-decode.8

clean:
	rm -f *.8 cw-decode

