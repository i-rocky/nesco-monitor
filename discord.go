package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

const (
	colorOK      = 0x2ecc71
	colorWarn    = 0xe74c3c
	colorNeutral = 0x3498db
)

type embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []embedField `json:"fields,omitempty"`
	Footer      *embedFooter `json:"footer,omitempty"`
	Image       *embedImage  `json:"image,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
}

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type embedFooter struct {
	Text string `json:"text"`
}

type embedImage struct {
	URL string `json:"url"`
}

type webhookPayload struct {
	Username  string  `json:"username,omitempty"`
	AvatarURL string  `json:"avatar_url,omitempty"`
	Content   string  `json:"content,omitempty"`
	Embeds    []embed `json:"embeds,omitempty"`
}

// attachment pairs a filename with its bytes for multipart upload.
type attachment struct {
	Name string
	Data []byte
}

type DiscordPoster struct {
	webhookURL string
	client     *http.Client
}

func NewDiscordPoster(webhookURL string, timeout time.Duration) *DiscordPoster {
	return &DiscordPoster{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: timeout},
	}
}

// PostBalance builds the daily report — main embed (balance + account +
// delta-since-yesterday) with the monthly spending chart attached. If
// balance is below threshold a red warning embed is prepended and @here
// is pinged.
func (d *DiscordPoster) PostBalance(
	ctx context.Context,
	snap *BalanceSnapshot,
	prev float64, hasPrev bool, prevTime time.Time,
	chartPNG []byte,
	threshold float64,
	tz *time.Location,
) error {
	now := snap.FetchedAt.In(tz)
	const chartName = "spending_monthly.png"

	pending := pendingCredit(snap, prev, prevTime, hasPrev)
	main := buildMainEmbed(snap, prev, hasPrev, pending, threshold, now, "attachment://"+chartName)
	embeds := []embed{main}
	files := []attachment{{Name: chartName, Data: chartPNG}}

	var content string
	if snap.Balance < threshold {
		warn := buildWarningEmbed(snap, threshold)
		embeds = append([]embed{warn}, embeds...)
		content = "@here Low balance warning"
	}

	payload := webhookPayload{
		Username: "NESCO Monitor",
		Content:  content,
		Embeds:   embeds,
	}
	return d.postMultipart(ctx, payload, files)
}

func buildMainEmbed(snap *BalanceSnapshot, prev float64, hasPrev bool, pending float64, threshold float64, now time.Time, imageURL string) embed {
	c := colorNeutral
	if snap.Balance < threshold {
		c = colorWarn
	} else if snap.Balance >= threshold*2 {
		c = colorOK
	}

	deltaLine := "First reading — no delta yet."
	if hasPrev {
		delta := snap.Balance - prev
		sign := "-"
		if delta >= 0 {
			sign = "+"
		}
		deltaLine = fmt.Sprintf("Change since last poll: **%s%.2f BDT** (was %.2f)", sign, abs(delta), prev)
	}
	if pending > 0 {
		deltaLine += fmt.Sprintf("\n+%.2f BDT recharge pending → ~%.2f effective", pending, snap.Balance+pending)
	}

	fields := []embedField{
		{Name: "Balance", Value: fmt.Sprintf("**%.2f BDT**", snap.Balance), Inline: true},
		{Name: "Account", Value: snap.AccountNo, Inline: true},
	}

	return embed{
		Title:       "NESCO Balance Update",
		Description: deltaLine,
		Color:       c,
		Fields:      fields,
		Image:       &embedImage{URL: imageURL},
		Footer:      &embedFooter{Text: "Asia/Dhaka — " + now.Format("Mon 02 Jan 2006 15:04 MST")},
		Timestamp:   snap.FetchedAt.UTC().Format(time.RFC3339),
	}
}

func buildWarningEmbed(snap *BalanceSnapshot, threshold float64) embed {
	return embed{
		Title: "LOW BALANCE WARNING",
		Description: fmt.Sprintf(
			"Account **%s** is at **%.2f BDT**, below the **%.0f BDT** threshold. Recharge soon to avoid disconnection.",
			snap.AccountNo, snap.Balance, threshold,
		),
		Color: colorWarn,
	}
}

// postMultipart uploads payload_json + files[0..N-1] per Discord's webhook spec.
// Each attachment's name must match an embed's "attachment://<name>" URL.
func (d *DiscordPoster) postMultipart(ctx context.Context, payload webhookPayload, files []attachment) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	jh := make(textproto.MIMEHeader)
	jh.Set("Content-Disposition", `form-data; name="payload_json"`)
	jh.Set("Content-Type", "application/json")
	jp, err := w.CreatePart(jh)
	if err != nil {
		return err
	}
	if _, err := jp.Write(jsonBytes); err != nil {
		return err
	}

	for i, f := range files {
		fh := make(textproto.MIMEHeader)
		fh.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files[%d]"; filename=%q`, i, f.Name))
		fh.Set("Content-Type", "image/png")
		fp, err := w.CreatePart(fh)
		if err != nil {
			return err
		}
		if _, err := fp.Write(f.Data); err != nil {
			return err
		}
	}

	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.webhookURL, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord webhook %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// pendingCredit returns the energy of recharges purchased after the previous
// reading whose credit is not yet reflected in the current balance (balance
// did not rise vs the previous reading). Used to show the effective balance
// during NESCO's update lag.
func pendingCredit(snap *BalanceSnapshot, prevBalance float64, prevTime time.Time, hasPrev bool) float64 {
	if !hasPrev || snap.Balance > prevBalance {
		return 0
	}
	var sum float64
	for _, r := range snap.Recharges {
		if r.PurchasedAt.After(prevTime) {
			sum += r.EnergyAmount
		}
	}
	return sum
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

