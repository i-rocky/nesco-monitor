package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Labels rendered by the NESCO panel when ?lang=en. Used as map keys after
// the HTML walk pairs each <label> with the following <input value="...">.
const (
	labelAccount = "Consumer No."
	labelMeter   = "Meter No."
	labelBalance = "Remaining Balance (Tk.)"
	labelName    = "Consumer Name"
	labelAddress = "Address"
	labelOffice  = "Concern Office"
	labelMinRech = "Minimum Recharge Amount (Tk.)"
	labelLoad    = "Sanction Load (K.W)"
	labelTariff  = "Sanction Tariff"
)

const (
	pathLanguageEn = "/language/en"
	pathPanel      = "/pre/panel"
	submitButton   = "Recharge History"
	paramToken     = "_token"
	paramCustNo    = "cust_no"
	paramSubmit    = "submit"
)

// BalanceSnapshot is one point-in-time view of an account.
type BalanceSnapshot struct {
	AccountNo       string
	MeterNo         string
	ConsumerName    string
	Address         string
	Office          string
	Balance         float64 // taka
	MinRecharge     float64 // taka
	SanctionLoadKW  string
	SanctionTariff  string
	FetchedAt       time.Time // wall clock at fetch (UTC)
	RawFields       map[string]string
	Recharges       []Recharge // full recharge history scraped from the same page
}

// Recharge is one row of the panel's recharge-history table. EnergyAmount
// (data-purchaseamount) is the value credited to "Remaining Balance"; that's
// what consumption math must add back. OrderID is the stable dedup key.
type Recharge struct {
	OrderID      string
	Token        string
	EnergyAmount float64
	PaidAmount   float64
	PurchasedAt  time.Time
}

// NescoClient is a tiny HTTP scraper of the customer self-service panel.
// It is intentionally separate from the main project so this directory
// can be vendored or moved without dragging the rest of the repo.
type NescoClient struct {
	base   string
	client *http.Client
}

func NewNescoClient(baseURL string, timeout time.Duration) *NescoClient {
	jar, _ := cookiejar.New(nil)
	return &NescoClient{
		base: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: timeout,
			Jar:     jar,
		},
	}
}

// GetBalance runs the 3-step ritual: switch session to English, scrape the
// csrf-token meta tag from the panel page, then POST the consumer number.
func (c *NescoClient) GetBalance(ctx context.Context, accountNo string) (*BalanceSnapshot, error) {
	if err := c.switchToEnglish(ctx); err != nil {
		return nil, fmt.Errorf("switch language: %w", err)
	}
	token, err := c.getCSRFToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("csrf: %w", err)
	}
	body, err := c.postBalance(ctx, accountNo, token)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer body.Close()
	return parseBalancePage(body)
}

func (c *NescoClient) switchToEnglish(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+pathLanguageEn, nil)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *NescoClient) getCSRFToken(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+pathPanel, nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	var token string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if token != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "meta" {
			var name, content string
			for _, a := range n.Attr {
				switch a.Key {
				case "name":
					name = a.Val
				case "content":
					content = a.Val
				}
			}
			if name == "csrf-token" && content != "" {
				token = content
				return
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)

	if token == "" {
		return "", fmt.Errorf("csrf-token meta tag not found")
	}
	return token, nil
}

func (c *NescoClient) postBalance(ctx context.Context, custNo, token string) (io.ReadCloser, error) {
	form := url.Values{}
	form.Set(paramToken, token)
	form.Set(paramCustNo, custNo)
	form.Set(paramSubmit, submitButton)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+pathPanel,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// parseBalancePage walks the response HTML pairing each <label>'s visible text
// with the next <input value="...">. NESCO uses a fixed set of label strings
// so we just look those up. Extra labels (consumer name, address, etc.) are
// kept in RawFields for the embed body.
func parseBalancePage(body io.Reader) (*BalanceSnapshot, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	data := map[string]string{}
	var currentLabel string

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "label" {
				// concatenate immediate text children (skip <small>, <span>, <br> etc.)
				var b strings.Builder
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						b.WriteString(c.Data)
					}
				}
				currentLabel = strings.TrimSpace(strings.ReplaceAll(b.String(), "\n", " "))
			}
			if n.Data == "input" && currentLabel != "" {
				for _, a := range n.Attr {
					if a.Key == "value" {
						data[currentLabel] = strings.TrimSpace(a.Val)
						currentLabel = ""
						break
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(data) == 0 {
		return nil, fmt.Errorf("no fields extracted (account may not exist or panel layout changed)")
	}

	acc, okA := data[labelAccount]
	balStr, okB := data[labelBalance]
	if !okA || !okB {
		return nil, fmt.Errorf("missing required fields (have %d labels): account=%v balance=%v",
			len(data), okA, okB)
	}

	bal, err := strconv.ParseFloat(strings.TrimSpace(balStr), 64)
	if err != nil {
		return nil, fmt.Errorf("parse balance %q: %w", balStr, err)
	}

	minRech, _ := strconv.ParseFloat(strings.TrimSpace(data[labelMinRech]), 64)

	return &BalanceSnapshot{
		AccountNo:      acc,
		MeterNo:        data[labelMeter],
		ConsumerName:   data[labelName],
		Address:        data[labelAddress],
		Office:         data[labelOffice],
		Balance:        bal,
		MinRecharge:    minRech,
		SanctionLoadKW: data[labelLoad],
		SanctionTariff: data[labelTariff],
		FetchedAt:      time.Now().UTC(),
		RawFields:      data,
		Recharges:      parseRecharges(doc),
	}, nil
}

// rechargeDateLayout matches data-purchasedate, e.g. "21-JUN-2026 1:17 PM".
// Go matches month names case-insensitively, so uppercase "JUN" parses.
const rechargeDateLayout = "02-Jan-2006 3:04 PM"

// parseRecharges walks the panel DOM for <a class="consumerRechargeData">
// rows and returns the recharge history.
func parseRecharges(doc *html.Node) []Recharge {
	dhaka, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		dhaka = time.UTC
	}
	var out []Recharge
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "consumerRechargeData") {
			if r, ok := rechargeFromNode(n, dhaka); ok {
				out = append(out, r)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, f := range strings.Fields(a.Val) {
				if f == class {
					return true
				}
			}
		}
	}
	return false
}

func rechargeFromNode(n *html.Node, loc *time.Location) (Recharge, bool) {
	attr := map[string]string{}
	for _, a := range n.Attr {
		attr[a.Key] = a.Val
	}
	order := attr["data-order"]
	if order == "" {
		return Recharge{}, false
	}
	energy, _ := strconv.ParseFloat(strings.TrimSpace(attr["data-purchaseamount"]), 64)
	paid, _ := strconv.ParseFloat(strings.TrimSpace(attr["data-totalamount"]), 64)
	ts, err := time.ParseInLocation(rechargeDateLayout, strings.TrimSpace(attr["data-purchasedate"]), loc)
	if err != nil {
		return Recharge{}, false
	}
	return Recharge{
		OrderID:      order,
		Token:        attr["data-token"],
		EnergyAmount: energy,
		PaidAmount:   paid,
		PurchasedAt:  ts.UTC(),
	}, true
}
