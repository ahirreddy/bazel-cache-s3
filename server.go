package main

import (
	"log"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/golang/groupcache"
	"github.com/pkg/errors"
)

func logCacheStats(group *groupcache.Group, interval time.Duration) {
	for t := time.Tick(interval); ; <-t {
		log.Printf("Stats | %+v", group.Stats)
		log.Printf("CacheStats:MainCache | %+v", group.CacheStats(groupcache.MainCache))
		log.Printf("CacheStats:HotCache | %+v", group.CacheStats(groupcache.HotCache))
	}
}

type cacheServer struct {
	s3m *S3Manager

	group *groupcache.Group
	gpool *groupcache.HTTPPool
}

// newCacheServer provides an HTTP server that implements a bazel cache endpoint. It uses an S3Manager to store cachable actions and objects into S3nd a groupcache pool to cache objects
func newCacheServer(s3m *S3Manager, self string, updater Updater) *cacheServer {
	// Create group of cached objects
	group := groupcache.NewGroup(
		"bazelcache",
		2<<32,
		groupcache.GetterFunc(s3m.Getter),
	)
	go logCacheStats(group, time.Second*15)

	// Find our peers
	pool := groupcache.NewHTTPPoolOpts(self, nil)
	go func() {
		if err := updater(pool); err != nil {
			log.Fatal(errors.Wrap(err, "updater failed"))
		}
	}()

	cs := &cacheServer{
		s3m: s3m,

		group: group,
		gpool: pool,
	}

	return cs
}

func (c *cacheServer) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	m := chi.NewRouter()

	// tempDir, err := ioutil.TempDir("", "bazelcache")
	// if err != nil {
	// 	log.Println(errors.Wrap(err, "failed to make tempdir for body caching"))
	// }
	// defer os.RemoveAll(tempDir)

	m.Use(
		middleware.GetHead,
		middleware.Logger,
		middleware.Recoverer,
	)

	m.Handle("/_groupcache", c.gpool)

	m.Get("/ac/*", c.handleGET())
	m.Get("/cas/*", c.handleGET())
	// m.Put("/", diskBufferBodies(tempDir, c.handlePUT())
	m.Put("/ac/*", c.handlePUT())
	m.Put("/cas/*", c.handlePUT())

	m.ServeHTTP(rw, r)
}

func (c *cacheServer) handleGET() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		key := r.URL.Path[1:]

		var b groupcache.ByteView
		err := c.group.Get(r.Context(), key, groupcache.ByteViewSink(&b))
		if err := errors.Cause(err); err != nil {
			if awsErr, ok := err.(awserr.RequestFailure); ok && awsErr.StatusCode() == http.StatusNotFound {
				http.NotFound(rw, r)
				return
			}

			log.Println(errors.Wrap(err, "http get request failed"))
			http.Error(rw, "failed to retrieve key", http.StatusInternalServerError)
		}

		http.ServeContent(rw, r, key, time.Unix(0, 0), b.Reader())
	}
}

func (c *cacheServer) handlePUT() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		key := r.URL.Path[1:]

		c.s3m.PutReader(r.Context(), key, r.Body)
	}
}
