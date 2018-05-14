package main

import (
	"flag"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

var (
	bind = flag.String("bind", "127.0.0.1:8080", "bind to this socket")

	self = flag.String("self", "http://localhost:8080", "This should be a valid base URL that points to the current server, for example \"http://example.net:8000\".")

	manualPeers = flag.String("peers", "", "CSV separated list of peers' URLs")
	srvDNSName  = flag.String("peer-srv-endpoint", "", "SRV record prefix for peer discovery (intended for use with kubernetes headless services)")
	u           updater

	bucket = flag.String("bucket", "", "Bucket ot use for S3 client")
)

func parseArgs() {
	flag.Parse()

	if *bucket == "" {
		log.Fatal("-bucket is required")
	}

	if _, err := url.Parse(*self); err != nil {
		log.Fatalf("-self=%q does not contain a valid URL: %s", *self, err)
	}

	if *manualPeers != "" && *srvDNSName != "" {
		log.Fatal("-peers & -peer-srv-endpoint are mututally exclusive options")
	}

	if peers := strings.Split(*manualPeers, ","); len(peers) > 0 {
		for _, p := range peers {
			_, err := url.Parse(p)
			if err != nil {
				log.Fatalf("%q is not a valid URL", p)
			}
		}
	}

	switch {
	case *manualPeers != "":
		u = StaticPeers(append(strings.Split(*manualPeers, ","), *self))
	case *srvDNSName != "":
		u = SRVDiscoveredPeers(*self, *srvDNSName, time.Duration(0))
	}
}

func main() {
	parseArgs()

	s3m := NewS3Manager(
		s3.New(session.Must(session.NewSession(&aws.Config{
			Region:           aws.String("us-west-2"),
			S3ForcePathStyle: aws.Bool(true),
			Endpoint:         aws.String("http://localhost:9000"),
		}))),
		*bucket,
	)

	cs := newCacheServer(s3m, *self, u)

	http.ListenAndServe(*bind, cs)
}
