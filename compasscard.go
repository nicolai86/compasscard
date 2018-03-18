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
	BalanceDetails float64
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

func parseAmount(amount string) (float64, error) {
	val := strings.Replace(amount, "$", "", -1)
	if val == "" {
		return 0.0, nil
	}
	return strconv.ParseFloat(val, 64)
}

func parseUsageRecord(line []string) (*UsageRecord, error) {
	t, err := time.Parse(usageRecordLayout, line[0])
	if err != nil {
		return nil, err
	}
	amount, err := parseAmount(line[4])
	if err != nil {
		fmt.Println(line[4])
		return nil, err
	}
	balance, err := parseAmount(line[5])
	if err != nil {
		fmt.Println(line[5])
		return nil, err
	}
	return &UsageRecord{
		DateTime:       t,
		Transaction:    line[1],
		Product:        line[2],
		LineItem:       line[3],
		Amount:         amount,
		BalanceDetails: balance,
		OrderDate:      line[6],
		Payment:        line[7],
		OrderNumber:    line[8],
		AuthCode:       line[9],
		Total:          line[10],
	}, nil
}

const usageDateLayout = "02/01/2006 15:04:05 PM"

// Parse converts a compass card csv response into UsageRecords
func Parse(raw []byte) ([]UsageRecord, error) {
	r := csv.NewReader(strings.NewReader(string(raw)))
	header := true
	lines := []UsageRecord{}
	for {
		line, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header {
			header = !header
			continue
		}

		record, err := parseUsageRecord(line)
		if err != nil {
			return nil, err
		}
		lines = append(lines, *record)
	}
	return lines, nil
}

// Cards loads all available cards from your compasscard account
func (s *Session) Cards() ([]string, error) {
	resp, err := s.client.Get(fmt.Sprintf("%s/ManageCards", endpoint))
	if err != nil {
		return nil, err
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	ids := []string{}
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			isCard := false
			for _, attr := range n.Attr {
				if attr.Key == "id" && attr.Val == "Content_ManageCard_hfSerialNo" {
					isCard = true
					break
				}
			}
			if isCard {
				for _, attr := range n.Attr {
					if attr.Key == "value" {
						ids = append(ids, attr.Val)
						break
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	return ids, nil
}

// Usage looks up a specific compasscard usage
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

	lines, err := Parse(bs)
	if err != nil {
		return nil, nil, err
	}
	return lines, bs, nil
}

func (s *Session) login(username, password string) error {
	form := url.Values{}
	form.Add("__CSRFTOKEN", s.csrfToken)
	form.Add("__EVENTTARGET", "")
	form.Add("__EVENTARGUMENT", "")
	form.Add("ctl00$txtSignInEmail", "")
	form.Add("ctl00$txtSignInPassword", "")
	form.Add("ctl00$Content$passwordInfo$email", "")
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

// TODO add SignOut call to session
func (s *Session) Signout() error {
	form := url.Values{}
	form.Add("__CSRFTOKEN", s.csrfToken)
	form.Add("__VIEWSTATE", s.evntState)
	form.Add("__EVENTTARGET", "ctl00$btnSignOut")
	form.Add("__EVENTARGUMENT", "")
	form.Add("__VIEWSTATEGENERATOR", s.evntGenerator)
	form.Add("__EVENTVALIDATION", s.evntValidation)
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/ManageCards", endpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

type ClientOption interface {
	Apply(*Session)
}

type ClientOptionFunc func(*Session)

func (fnc ClientOptionFunc) Apply(s *Session) {
	fnc(s)
}

func WithCookieJar(jar *cookiejar.Jar) ClientOption {
	return ClientOptionFunc(func(s *Session) {
		s.client.Jar = jar
	})
}

func New(username, password string, options ...ClientOption) (*Session, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
	}

	s := &Session{
		client: client,
	}
	for _, opt := range options {
		opt.Apply(s)
	}
	if err := s.populateCSRF(); err != nil {
		return nil, err
	}
	if err := s.login(username, password); err != nil {
		return nil, err
	}
	return s, nil
}
