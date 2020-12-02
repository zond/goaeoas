package goaeoas

import (
	"net/http"

	"google.golang.org/appengine"
)

func VersionETagCache(handler func(ResponseWriter, Request) error) func(ResponseWriter, Request) error {
	return func(w ResponseWriter, r Request) error {
		etag := appengine.VersionID(appengine.NewContext(r.Req()))
		if r.Req().Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return nil
		}
		w.Header().Set("ETag", etag)
		return handler(w, r)
	}
}
