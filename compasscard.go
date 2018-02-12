package compasscard

import (
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const endpoint = "https://www.compasscard.ca"

type Session struct {
	client *http.Client

	csrfToken      string // __CSRFTOKEN
	evntValidation string // __EVENTVALIDATION
	evntState      string // __VIEWSTATE
	evntGenerator  string // __VIEWSTATEGENERATOR
}

func captureInput(name string, val *string, n *html.Node) {
	matches := false
	for _, attr := range n.Attr {
		matches = matches || (attr.Key == "name" && attr.Val == name)
	}
	if !matches {
		return
	}
	for _, attr := range n.Attr {
		if attr.Key == "value" {
			*val = attr.Val
		}
	}
}

func (s *Session) populateCSRF() error {
	resp, err := s.client.Get(fmt.Sprintf("%s/SignIn", endpoint))
	if err != nil {
		return err
	}
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return err
	}

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			captureInput("__CSRFTOKEN", &s.csrfToken, n)
			captureInput("__EVENTVALIDATION", &s.evntValidation, n)
			captureInput("__VIEWSTATE", &s.evntState, n)
			captureInput("__VIEWSTATEGENERATOR", &s.evntGenerator, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	return nil
}

type UsageRecord struct {
	DateTime       time.Time
	Transaction    string
	Product        string
	LineItem       string
	Amount         float64
	BalanceDetails string
	OrderDate      string
	Payment        string
	OrderNumber    string
	AuthCode       string
	Total          string
}

type UsageOptions struct {
	StartDate time.Time
	EndDate   time.Time
}

const usageRecordLayout = "Jan-02-2006 15:04 PM" // Jan-30-2018 06:08 PM

func parseUsageRecord(line []string) (*UsageRecord, error) {
	t, err := time.Parse(usageRecordLayout, line[0])
	if err != nil {
		return nil, err
	}
	val, err := strconv.ParseFloat(strings.Replace(line[4], "$", "", -1), 64)
	if err != nil {
		return nil, err
	}
	return &UsageRecord{
		DateTime:       t,
		Transaction:    line[1],
		Product:        line[2],
		LineItem:       line[3],
		Amount:         val,
		BalanceDetails: line[5],
		OrderDate:      line[6],
		Payment:        line[7],
		OrderNumber:    line[8],
		AuthCode:       line[9],
		Total:          line[10],
	}, nil
}

const usageDateLayout = "02/01/2006 15:04:05 PM"

func (s *Session) Usage(ccsn string, opts UsageOptions) ([]UsageRecord, []byte, error) {
	q := url.Values{}
	q.Set("type", "2")
	q.Set("start", opts.StartDate.Format(usageDateLayout))
	q.Set("end", opts.EndDate.Format(usageDateLayout))
	q.Set("ccsn", ccsn)
	q.Set("csv", "true")
	resp, err := s.client.Get(fmt.Sprintf(
		"https://www.compasscard.ca/handlers/compasscardusagepdf.ashx?%s",
		q.Encode(),
	),
	)
	if err != nil {
		return nil, nil, err
	}

	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	r := csv.NewReader(strings.NewReader(string(bs)))
	header := true
	lines := []UsageRecord{}
	for {
		line, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if header {
			header = !header
			continue
		}

		record, err := parseUsageRecord(line)
		if err != nil {
			return nil, nil, err
		}
		lines = append(lines, *record)
	}
	return lines, bs, nil
}

func (s *Session) login(username, password string) error {
	form := url.Values{}
	form.Add("__CSRFTOKEN", s.csrfToken)
	form.Add("__VIEWSTATE", s.evntState)
	form.Add("__VIEWSTATEGENERATOR", s.evntGenerator)
	form.Add("__EVENTVALIDATION", s.evntValidation)
	form.Add("ctl00$Content$btnSignIn", "Sign in")
	form.Add("ctl00$Content$emailInfo$txtEmail", username)
	form.Add("ctl00$Content$passwordInfo$txtPassword", password)
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/SignIn", endpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// bs, err := ioutil.ReadAll(resp.Body)
	// if err != nil {
	// 	return err
	// }

	return nil
}

func New(username, password string) (*Session, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
	}

	s := &Session{
		client: client,
	}
	if err := s.populateCSRF(); err != nil {
		return nil, err
	}
	if err := s.login(username, password); err != nil {
		return nil, err
	}
	return s, nil
}
