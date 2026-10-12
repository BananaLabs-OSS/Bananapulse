package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSMTPVerifiedSTARTTLSAndCertificatePin(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "internal-mail.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(raw)
	path := filepath.Join(t.TempDir(), "smtp.pem")
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "220 internal-mail.test ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		encrypted := false
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250-internal-mail.test\r\n250 STARTTLS\r\n")
			case line == "STARTTLS":
				fmt.Fprint(conn, "220 Ready for TLS\r\n")
				conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{raw}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
				scanner = bufio.NewScanner(conn)
				encrypted = true
			case strings.HasPrefix(line, "MAIL FROM:") || strings.HasPrefix(line, "RCPT TO:"):
				if !encrypted {
					return
				}
				fmt.Fprint(conn, "250 Accepted\r\n")
			case line == "DATA":
				fmt.Fprint(conn, "354 Send data\r\n")
				var lines []string
				for scanner.Scan() {
					if scanner.Text() == "." {
						break
					}
					lines = append(lines, scanner.Text())
				}
				received <- strings.Join(lines, "\n")
				fmt.Fprint(conn, "250 queued\r\n")
			case line == "QUIT":
				fmt.Fprint(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_FROM", "noreply@example.test")
	t.Setenv("SMTP_TLS_SERVER_NAME", "internal-mail.test")
	t.Setenv("SMTP_TLS_CERT_FILE", path)
	sender, err := smtpSenderFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err = sender.Send(context.Background(), emailIntent{Recipient: "admin@example.test", Subject: "Sign in", Body: "private link"}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		if !strings.Contains(message, "Content-Transfer-Encoding: base64") {
			t.Fatal("body encoding missing")
		}
	case <-time.After(time.Second):
		t.Fatal("mail missing")
	}
	if err = sender.tls.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}); err != nil {
		t.Fatal(err)
	}
	wrong := *cert
	wrong.Raw = []byte("different certificate")
	if sender.tls.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{&wrong}}) == nil {
		t.Fatal("untrusted certificate accepted")
	}
	expired := *cert
	expired.NotAfter = time.Now().Add(-time.Second)
	if sender.tls.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{&expired}}) == nil {
		t.Fatal("expired certificate accepted")
	}
	t.Setenv("SMTP_TLS_SERVER_NAME", "wrong.test")
	sender, err = smtpSenderFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if sender.tls.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}) == nil {
		t.Fatal("wrong identity accepted")
	}
}
func TestSMTPRejectsHeaderInjection(t *testing.T) {
	if _, err := smtpMessage("noreply@example.test", "Status", "admin@example.test", "Login\r\nBcc: attacker@example.test", "body"); err == nil {
		t.Fatal("header injection accepted")
	}
}
