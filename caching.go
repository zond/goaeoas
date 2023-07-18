package goaeoas

import (
	"crypto/sha1"
	"fmt"
	"net/http"

	"google.golang.org/appengine/v2"
)

func VersionETagCache(handler func(ResponseWriter, Request) error) func(ResponseWriter, Request) error {
	return func(w ResponseWriter, r Request) error {
		media, charset := Media(r.Req(), "Accept")
		h := sha1.New()
		h.Write([]byte(fmt.Sprintf("version:%s,media:s,charset:s", appengine.VersionID(appengine.NewContext(r.Req())), media, charset)))
		etag := fmt.Sprintf("W/%x", h.Sum(nil))
		if r.Req().Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return nil
		}
		w.Header().Set("ETag", etag)
		return handler(w, r)
	}
}
