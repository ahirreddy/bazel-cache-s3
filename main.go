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
	"github.com/golang/groupcache"
)

var (
	bind = flag.String("bind", "127.0.0.1:8080", "bind to this socket")

	self = flag.String("self", "http://localhost:8080", "This should be a valid base URL that points to the current server, for example \"http://example.net:8000\".")

	manualPeers = flag.String("peers", "", "CSV separated list of peers' URLs")
	srvDNSName  = flag.String("peer-srv-endpoint", "", "SRV record prefix for peer discovery (intended for use with kubernetes headless services)")

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
}

func logCacheStats(group *groupcache.Group, interval time.Duration) {
	for t := time.Tick(interval); ; <-t {
		log.Printf("Stats | %+v", group.Stats)
		log.Printf("CacheStats:MainCache | %+v", group.CacheStats(groupcache.MainCache))
		log.Printf("CacheStats:HotCache | %+v", group.CacheStats(groupcache.HotCache))
	}
}

func main() {
	parseArgs()

	s3 := NewS3(
		s3.New(session.Must(session.NewSession(&aws.Config{
			Region:           aws.String("us-west-2"),
			S3ForcePathStyle: aws.Bool(true),
			Endpoint:         aws.String("http://localhost:9000"),
		}))),
		*bucket,
	)

	// Create group of cached objects
	group := groupcache.NewGroup(
		"bazelcache",
		2<<32,
		groupcache.GetterFunc(s3.Getter),
	)
	go logCacheStats(group, time.Second*10)

	// Find our peers
	pool := groupcache.NewHTTPPoolOpts(*self, nil)
	switch {
	case *manualPeers != "":
		peers := strings.Split(*manualPeers, ",")
		StaticPeers(pool, append(peers, *self))
	case *srvDNSName != "":
		go func() {
			err := SRVDiscoveredPeers(pool, *self, *srvDNSName)
			log.Fatal("SRV peer resolution has died: ", err)
		}()
	}

	http.Handle("/", http.HandlerFunc(bazelClientHandler(group, s3)))
	http.Handle("/_groupcache", pool)
	http.ListenAndServe(*bind, nil)
}
