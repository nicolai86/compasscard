package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/nicolai86/compasscard"
)

type server struct {
	username string
	password string
	tmpdir   string
	cache    map[string][]compasscard.UsageRecord
}

func isCurrentMonth(date time.Time) bool {
	now := time.Now()
	beginningOfMonth := now.AddDate(0, 0, -now.Day()-1)
	endOfMonth := beginningOfMonth.AddDate(0, 1, -1)
	return date.After(beginningOfMonth) && date.Before(endOfMonth)
}

// TODO type loader
func (s *server) lookup(date time.Time, ccsn string) ([]compasscard.UsageRecord, []byte, error) {
	sess, err := compasscard.New(s.username, s.password)
	if err != nil {
		return nil, nil, err
	}
	startDate := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, -1)
	records, raw, err := sess.Usage(ccsn, compasscard.UsageOptions{
		StartDate: startDate,
		EndDate:   endDate,
	})
	return records, raw, err
}

// TODO type cached loader
func (s *server) lookupAndCache(date time.Time, ccsn string) ([]compasscard.UsageRecord, error) {
	key := date.Format("2006-01")
	records, ok := s.cache[key]
	if ok {
		return records, nil
	}

	cacheFile := fmt.Sprintf("%s/%s-%s.csv", s.tmpdir, ccsn, key)

	bs, err := ioutil.ReadFile(cacheFile)
	if err == nil {
		records, err := compasscard.Parse(bs)
		if err != nil {
			return nil, err
		}
		s.cache[key] = records
		return records, nil
	}

	records, raw, err := s.lookup(date, ccsn)
	if err != nil {
		return nil, err
	}
	s.cache[key] = records

	err = ioutil.WriteFile(cacheFile, raw, 0644)

	return records, err
}

type response struct {
	Lines []compasscard.UsageRecord
	CCSN  string
}

func (s *server) handle(w http.ResponseWriter, ccsn string, records []compasscard.UsageRecord) {
	resp := response{
		CCSN:  ccsn,
		Lines: records,
	}
	json.NewEncoder(w).Encode(&resp)
}

// ServeHTTP handles GET /ccsn?year&month usage
func (s *server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ccsn := req.URL.Path
	year, err := strconv.Atoi(req.URL.Query().Get("year"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	month, err := strconv.Atoi(req.URL.Query().Get("month"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	if month < 1 || month > 12 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("month out of range [1, 12]"))
		return
	}

	date := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	if isCurrentMonth(date) {
		records, _, err := s.lookup(date, ccsn)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(err.Error()))
			return
		}
		s.handle(w, ccsn, records)
		return
	}

	records, err := s.lookupAndCache(date, ccsn)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	s.handle(w, ccsn, records)
}

func main() {
	username := flag.String("username", "", "compasscard.ca username")
	password := flag.String("password", "", "compasscard.ca password")
	tmpdir := flag.String("cache-dir", "/tmp", "directory to cache past months")
	listen := flag.String("listen", ":8080", "listen on port")
	flag.Parse()

	if *username == "" || *password == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	// TODO verify creds
	srv := server{
		username: *username,
		password: *password,
		tmpdir:   *tmpdir,
		cache:    make(map[string][]compasscard.UsageRecord),
	}
	http.Handle("/", http.StripPrefix("/", &srv))
	log.Printf("Listening on %q\n", *listen)
	http.ListenAndServe(*listen, http.DefaultServeMux)
}
