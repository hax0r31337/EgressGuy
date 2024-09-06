# EgressGuy

Generates egress bills to whose using S3 bucket to serve BLOBs.

## How to use

### One-click script

```bash
bash <(curl -L -s https://raw.githubusercontent.com/hax0r31337/EgressGuy/main/1clk.sh) -r "http://example.com/somefile"
```

### Build manually

```bash
# build the binary
go build -v -trimpath -ldflags "-s -w -buildid=" -o egressguy ./main

# print usage
./egressguy -h

sudo ./egressguy -r "http://example.com/somefile"
```

## How it works

The program establishes the connection and send http requests by sending raw TCP packets to the server.

As the program is intended to generate egress bills,
it does not need to receive all the data from the server,
it only need to trick the server to send the data to the client.

And by sending raw TCP packets, the program can trick the server to send data with the traffic higher than the client network speed.
