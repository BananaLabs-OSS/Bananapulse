package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

const eventHostSMTPEmail = "bananapulse.host.email.smtp.send.v1"

type smtpSender struct {
	address, host, from, name string
	tls                       *tls.Config
}

func smtpSenderFromEnv() (*smtpSender, error) {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		return nil, nil
	}
	from := strings.TrimSpace(os.Getenv("SMTP_FROM"))
	parsed, err := mail.ParseAddress(from)
	if err != nil || parsed.Address != from || strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("SMTP_FROM must be an email address")
	}
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "25"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, errors.New("invalid SMTP port")
	}
	serverName := strings.TrimSpace(os.Getenv("SMTP_TLS_SERVER_NAME"))
	if serverName == "" {
		serverName = host
	}
	tlsConfig := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if pinPath := os.Getenv("SMTP_TLS_CERT_FILE"); pinPath != "" {
		raw, err := os.ReadFile(pinPath)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, errors.New("invalid SMTP certificate pin")
		}
		pinned, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		pin := sha256.Sum256(pinned.Raw)
		// Explicit leaf pin supports this internal Mailcow's older CN-only cert.
		// The callback verifies exact certificate, identity and validity; there is
		// no unchecked TLS mode or public CA/hostname verification bypass.
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("missing SMTP certificate")
			}
			leaf := state.PeerCertificates[0]
			if sha256.Sum256(leaf.Raw) != pin {
				return errors.New("SMTP certificate pin mismatch")
			}
			if time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
				return errors.New("SMTP certificate outside validity period")
			}
			if len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0 {
				if leaf.Subject.CommonName != serverName {
					return errors.New("SMTP certificate identity mismatch")
				}
				return nil
			}
			return leaf.VerifyHostname(serverName)
		}
	}
	return &smtpSender{address: net.JoinHostPort(host, port), host: serverName, from: from, name: os.Getenv("SMTP_FROM_NAME"), tls: tlsConfig}, nil
}
func smtpMessage(from, name, to, subject, body string) (string, error) {
	parsed, err := mail.ParseAddress(to)
	if err != nil || parsed.Address != to || strings.ContainsAny(to+subject+name, "\r\n") {
		return "", errors.New("invalid mail headers")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	var lines []string
	for len(encoded) > 76 {
		lines = append(lines, encoded[:76])
		encoded = encoded[76:]
	}
	lines = append(lines, encoded)
	sender := (&mail.Address{Name: name, Address: from}).String()
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	domain := strings.SplitN(from, "@", 2)[1]
	return "From: " + sender + "\r\nTo: " + to + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\nMessage-ID: <" + hex.EncodeToString(id) + "@" + domain + ">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + strings.Join(lines, "\r\n") + "\r\n", nil
}
func (s *smtpSender) Send(ctx context.Context, intent emailIntent) error {
	message, err := smtpMessage(s.from, s.name, intent.Recipient, intent.Subject, intent.Body)
	if err != nil {
		return err
	}
	connection, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", s.address)
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer connection.Close()
	deadline := time.Now().Add(20 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		return errors.New("SMTP greeting failed")
	}
	defer client.Close()
	if err = client.StartTLS(s.tls); err != nil {
		return errors.New("SMTP verified STARTTLS failed")
	}
	if err = client.Mail(s.from); err != nil {
		return errors.New("SMTP sender rejected")
	}
	if err = client.Rcpt(intent.Recipient); err != nil {
		return errors.New("SMTP recipient rejected")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP data rejected")
	}
	if _, err = writer.Write([]byte(message)); err != nil {
		return errors.New("SMTP write failed")
	}
	if err = writer.Close(); err != nil {
		return errors.New("SMTP message not accepted")
	}
	// Acceptance after DATA is the delivery boundary; a failed QUIT must not
	// turn an already queued message into a duplicate retry.
	_ = client.Quit()
	return nil
}
func (b *httpBridge) smtpHTTP(ctx context.Context, r *http.Request, w http.ResponseWriter) error {
	if b.smtp == nil || !b.families.auth || b.token == "" {
		return disabledBridgeFamily()
	}
	var request struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}
	if err := decodeBridgeJSON(r, &request, false); err != nil {
		return err
	}
	if len(request.Text) > 100000 || len(request.Subject) > 200 {
		return &bridgeRequestError{message: "mail message too large"}
	}
	if err := b.smtp.Send(ctx, emailIntent{Recipient: request.To, Subject: request.Subject, Body: request.Text}); err != nil {
		return &bridgeDomainError{Status: 502, Code: "smtp_failed", Message: "Internal SMTP delivery failed."}
	}
	writeBridgeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
	return nil
}
