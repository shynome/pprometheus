package push

import (
	"io"
	"io/ioutil"
	"net/http"
	"time"
	"unsafe"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/textparse"
	"github.com/prometheus/prometheus/pkg/timestamp"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/tsdb"
)

type Server struct {
	GetAppender func() tsdb.Appender
}

func yoloString(b []byte) string {
	return *((*string)(unsafe.Pointer(&b)))
}

func (s Server) appender() tsdb.Appender {
	return s.GetAppender()
}

var endCloseBytes = []byte("{}")

// Append copy from scrape module
func (s Server) Append(b []byte, ts time.Time) (err error) {
	var (
		app = s.appender()
		p   = textparse.New(b, "text/plain")
		t   = timestamp.FromTime(ts)
	)

loop:
	for {
		var et textparse.Entry
		if et, err = p.Next(); err != nil {
			if err == io.EOF {
				err = nil
			}
			break
		}
		switch et {
		case textparse.EntryType:
			// fmt.Println(p.Type())
			continue
		case textparse.EntryHelp:
			// fmt.Println(p.Help())
			continue
		case textparse.EntrySeries:
			s, _, v := p.Series()
			var lb labels.Labels
			s = append(s, endCloseBytes...)
			if lb, err = promql.ParseMetric(yoloString(s)); err != nil {
				break
			}
			app.Add(lb, t, v)
			continue
		case textparse.EntryUnit:
			// fmt.Println(p.Unit())
			continue
		case textparse.EntryComment:
			// fmt.Println(string(p.Comment()))
			continue
		default:
			break loop
		}
	}
	if err != nil {
		return
	}
	err = app.Commit()
	return
}

func (s Server) ServeHTTP(res http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		res.WriteHeader(500)
		res.Write([]byte(""))
		return
	}
	err = s.Append(body, time.Now())
	if err != nil {
		res.WriteHeader(400)
		res.Write([]byte(err.Error()))
		return
	}
}
