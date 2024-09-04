# EgressGuy

Generates egress bills to whose using S3 bucket to serve BLOBs.

## How to use

```bash
# build the binary
go build -v -trimpath -ldflags "-s -w -buildid=" -o egressguy ./main

# print usage
./egressguy -h

sudo ./egressguy -r "http://h55na.gdl.easebar.com/identityv_setup_release_oversea_0112.exe"
```

## How it works

The program establishes the connection and send http requests by sending raw TCP packets to the server.

As the program is intended to generate egress bills,
it does not need to receive all the data from the server,
it only need to trick the server to send the data to the client.

And by sending raw TCP packets, the program can trick the server to send data with the traffic higher than the client network speed.
