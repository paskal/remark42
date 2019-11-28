package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/remark/backend/app/store"
)

func TestEmailNew(t *testing.T) {
	var testSet = []struct {
		name     string
		template bool
		err      bool
		errText  string
		params   EmailParams
	}{
		{name: "with connection error", template: true, err: true},
		{name: "with template parse error",
			err: true, errText: "can't parse message template: template: messageFromRequest:1: unexpected unclosed action in command",
			params: EmailParams{
				Host:          "test@host",
				Port:          1000,
				TLS:           true,
				From:          "test@from",
				Username:      "test@username",
				Password:      "test@password",
				TimeOut:       time.Second,
				MsgTemplate:   "{{",
				BufferSize:    10,
				FlushDuration: time.Second,
			}},
		{name: "with verification template validation error",
			err: true, errText: "can't parse verification template: template: messageFromRequest:1: unexpected unclosed action in command",
			template: true,
			params: EmailParams{
				Host:                 "test@host",
				Port:                 1000,
				TLS:                  true,
				From:                 "test@from",
				Username:             "test@username",
				Password:             "test@password",
				TimeOut:              time.Second,
				VerificationTemplate: "{{",
				BufferSize:           10,
				FlushDuration:        time.Second,
			}},
	}
	for _, d := range testSet {
		t.Run(d.name, func(t *testing.T) {
			email, err := NewEmail(d.params)

			if d.err && d.errText == "" {
				assert.Error(t, err)
			} else if d.err && d.errText != "" {
				assert.EqualError(t, err, d.errText)
			} else {
				assert.NoError(t, err)
			}

			assert.NotNil(t, email, "email returned")
			assert.NotNil(t, email.submit, "e.submit is created during initialisation")
			if d.template {
				assert.NotNil(t, email.msgTmpl, "e.template is set")
			} else {
				assert.Nil(t, email.msgTmpl, "e.template is not set")
			}
			if d.params.MsgTemplate == "" {
				assert.Equal(t, defaultEmailTemplate, email.EmailParams.MsgTemplate, "empty params.MsgTemplate changed to default")
			} else {
				assert.Equal(t, d.params.MsgTemplate, email.EmailParams.MsgTemplate, "params.MsgTemplate unchanged after creation")
			}
			if d.params.FlushDuration == 0 {
				assert.Equal(t, defaultFlushDuration, email.EmailParams.FlushDuration, "empty params.FlushDuration changed to default")
			} else {
				assert.Equal(t, d.params.FlushDuration, email.EmailParams.FlushDuration, "params.FlushDuration unchanged after creation")
			}
			if d.params.TimeOut == 0 {
				assert.Equal(t, defaultEmailTimeout, email.EmailParams.TimeOut, "empty params.TimeOut changed to default")
			} else {
				assert.Equal(t, d.params.TimeOut, email.EmailParams.TimeOut, "params.TimOut unchanged after creation")
			}
			if d.params.BufferSize == 0 {
				assert.Equal(t, 1, email.EmailParams.BufferSize, "empty params.BufferSize changed to default")
			} else {
				assert.Equal(t, d.params.BufferSize, email.EmailParams.BufferSize, "params.BufferSize unchanged after creation")
			}
			assert.Equal(t, d.params.From, email.EmailParams.From, "params.From unchanged after creation")
			assert.Equal(t, d.params.Host, email.EmailParams.Host, "params.Host unchanged after creation")
			assert.Equal(t, d.params.Username, email.EmailParams.Username, "params.Username unchanged after creation")
			assert.Equal(t, d.params.Password, email.EmailParams.Password, "params.Password unchanged after creation")
			assert.Equal(t, d.params.Port, email.EmailParams.Port, "params.Port unchanged after creation")
			assert.Equal(t, d.params.TLS, email.EmailParams.TLS, "params.TLS unchanged after creation")
		})
	}
}

func TestEmailSendErrors(t *testing.T) {
	var err error
	e := Email{EmailParams: EmailParams{FlushDuration: time.Second}}

	e.verifyTmpl, err = template.New("test").Parse("{{.Test}}")
	assert.NoError(t, err)
	assert.EqualError(t, e.Send(context.Background(), Request{Email: "bad@example.org", Verification: VerificationMetadata{Token: "some"}}),
		"error executing template to build verifying message from request: template: test:1:2: executing \"test\" at <.Test>: can't evaluate field Test in type notify.verifyTmplData")
	e.verifyTmpl, err = template.New("test").Parse(defaultEmailVerificationTemplate)
	assert.NoError(t, err)

	e.msgTmpl, err = template.New("test").Parse("{{.Test}}")
	assert.NoError(t, err)
	assert.EqualError(t, e.Send(context.Background(), Request{Comment: store.Comment{ID: "999"}, parent: store.Comment{User: store.User{ID: "test"}}, Email: "bad@example.org"}),
		"error executing template to build message from request: template: test:1:2: executing \"test\" at <.Test>: can't evaluate field Test in type notify.msgTmplData")
	e.msgTmpl, err = template.New("test").Parse(defaultEmailTemplate)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.EqualError(t, e.Send(ctx, Request{Comment: store.Comment{ID: "999"}, parent: store.Comment{User: store.User{ID: "test"}}, Email: "bad@example.org"}),
		"sending message to \"bad@example.org\" aborted due to canceled context")
}

func TestEmailSend(t *testing.T) {
	const filledEmail = "From: test_sender\nTo: good_example@example.org\n" +
		"Subject: New comment for \"test title\"\nMIME-version: 1.0;\nContent-Type: text/html;" +
		" charset=\"UTF-8\";\n\ntest user name → test parent user name\n\n" +
		"test comment orig\n\n↦ <a href=\"http://test#remark42__comment-1\">test title</a>\n"
	const filledVerifyEmail = "From: test_sender\nTo: another@example.org\n" +
		"Subject: Email verification\nMIME-version: 1.0;\nContent-Type: text/html;" +
		" charset=\"UTF-8\";\n\nConfirmation for u another@example.org, site s\n\nToken: t\n"
	email, err := NewEmail(EmailParams{BufferSize: 3, From: "test_sender", FlushDuration: time.Millisecond * 200})
	assert.Error(t, err, "error match expected")
	assert.NotNil(t, email, "expecting email returned")
	// prevent triggering e.autoFlush creation
	email.once.Do(func() {})
	var testMessages []emailMessage
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		testMessages = append(testMessages, <-email.submit, <-email.submit)
		waitGroup.Add(-len(testMessages))
	}()
	assert.NoError(t, email.Send(context.Background(),
		Request{
			Comment: store.Comment{
				ID: "1", Orig: "test comment orig",
				User:      store.User{Name: "test user name"},
				Locator:   store.Locator{URL: "http://test"},
				PostTitle: "test title"},
			parent: store.Comment{
				User: store.User{
					Name: "test parent user name",
				}},
			Email: "good_example@example.org",
		}))
	assert.NoError(t, email.Send(context.Background(), Request{
		Email: "another@example.org",
		Verification: VerificationMetadata{
			Locator: store.Locator{SiteID: "s"},
			User:    "u",
			Token:   "t",
		},
	}))
	waitGroup.Wait()
	assert.Equal(t, 2, len(testMessages))
	assert.Equal(t, emailMessage{message: filledEmail, to: "good_example@example.org"}, testMessages[0])
	assert.Equal(t, emailMessage{message: filledVerifyEmail, to: "another@example.org"}, testMessages[1])
}

func TestEmailSend_ExitConditions(t *testing.T) {
	email, err := NewEmail(EmailParams{})
	assert.Error(t, err, "error match expected")
	assert.NotNil(t, email, "expecting email returned")
	// prevent triggering e.autoFlush creation
	emptyRequest := Request{Comment: store.Comment{ID: "999"}}
	assert.Nil(t, email.Send(context.Background(), emptyRequest),
		"Message without parent comment User.Email is not sent and returns nil")
	requestWithEqualUsersWithEmails := Request{Comment: store.Comment{ID: "999"}, Email: "good_example@example.org"}
	assert.Nil(t, email.Send(context.Background(), requestWithEqualUsersWithEmails),
		"Message with parent comment User equals comment User is not sent and returns nil")
}

func TestEmailSendAndAutoFlush(t *testing.T) {
	const emptyEmail = "From: test_sender\nTo: test@example.org\nSubject: New comment\nMIME-version: 1.0;" +
		"\nContent-Type: text/html; charset=\"UTF-8\";\n\n\n\n\n\n" +
		"↦ <a href=\"#remark42__comment-999\">original comment</a>\n"
	var testSet = []struct {
		name                string
		smtp                *fakeTestSMTP
		request             Request
		amount, quitCount   int
		mail, rcpt          string
		response, response2 string
		waitForTicker       bool
	}{
		{name: "single message: still in buffer at the time context is closed, not sent", smtp: &fakeTestSMTP{}, amount: 1, quitCount: 0,
			request: Request{Comment: store.Comment{ID: "999"}, parent: store.Comment{User: store.User{ID: "test"}}, Email: "test@example.org"}},
		{name: "four messages: three sent with failure, one discarded", smtp: &fakeTestSMTP{fail: map[string]bool{"data": true}}, amount: 4, quitCount: 1, mail: "test_sender",
			rcpt: "test@example.org", request: Request{Comment: store.Comment{ID: "999"}, parent: store.Comment{User: store.User{ID: "test"}}, Email: "test@example.org"}},
		{name: "four messages: three sent, one discarded", smtp: &fakeTestSMTP{}, amount: 4, quitCount: 1, mail: "test_sender",
			rcpt: "test@example.org", request: Request{Comment: store.Comment{ID: "999"}, parent: store.Comment{User: store.User{ID: "test"}}, Email: "test@example.org"},
			response: strings.Repeat(emptyEmail, 3)},
		{name: "10 messages: 1 abandoned by context exit", smtp: &fakeTestSMTP{}, amount: 10, quitCount: 3,
			rcpt: "test@example.org", request: Request{Comment: store.Comment{ID: "999"}, parent: store.Comment{User: store.User{ID: "test"}}, Email: "test@example.org"},
			mail: "test_sender", response: strings.Repeat(emptyEmail, 9)},
		{name: "one message: sent by timer", smtp: &fakeTestSMTP{}, amount: 1, quitCount: 0, waitForTicker: true,
			request: Request{Comment: store.Comment{ID: "999"}, parent: store.Comment{User: store.User{ID: "test"}}, Email: "test@example.org"}},
	}
	for _, d := range testSet {
		t.Run(d.name, func(t *testing.T) {
			email, err := NewEmail(EmailParams{BufferSize: 3, From: "test_sender", FlushDuration: time.Millisecond * 200})
			assert.Error(t, err, "error match expected")
			assert.NotNil(t, email, "email returned")

			email.smtpClient = d.smtp
			waitCh := make(chan int)
			ctx, cancel := context.WithCancel(context.Background())
			var waitGroup sync.WaitGroup

			// accumulate messages in parallel
			for i := 1; i <= d.amount; i++ {
				waitGroup.Add(1)
				i := i
				go func() {
					// will start once we close the channel
					<-waitCh
					assert.NoError(t, email.Send(ctx, d.request), fmt.Sprint(i))
					waitGroup.Done()
				}()
			}
			close(waitCh)
			waitGroup.Wait()
			readCount := d.smtp.readQuitCount()
			assert.Equal(t, d.quitCount, d.smtp.readQuitCount(), "connection closed expected amount of times")
			assert.Equal(t, d.rcpt, d.smtp.readRcpt(), "email receiver match expected")
			assert.Equal(t, d.mail, d.smtp.readMail(), "email sender match expected ")
			assert.Equal(t, d.response, d.smtp.buff.String(), "connection closed expected amount of times")
			if !d.waitForTicker {
				cancel()
			}
			// d.smtp.Quit() called either when context is closed or by timer
			for d.smtp.readQuitCount() < readCount+1 {
				time.Sleep(time.Millisecond * 100)
				// wait for another batch of email being sent
			}
			assert.Equal(t, d.quitCount+1, d.smtp.readQuitCount(), "connection closed expected amount of times")
			cancel()
			assert.Equal(t, d.quitCount+1, d.smtp.readQuitCount(),
				"second context cancel (or context cancel after timer sent messages) don't cause another try of sending messages")
		})
	}
}

func TestEmailSendBufferClientError(t *testing.T) {
	var testSet = []struct {
		name string
		smtp *fakeTestSMTP
		err  string
	}{
		{name: "failed to verify receiver", smtp: &fakeTestSMTP{fail: map[string]bool{"mail": true}},
			err: "problems with sending messages: 1 error occurred:\n\t* can't send message to : bad from address \"\": failed to verify sender\n\n"},
		{name: "failed to verify sender", smtp: &fakeTestSMTP{fail: map[string]bool{"rcpt": true}},
			err: "problems with sending messages: 1 error occurred:\n\t* can't send message to : bad to address \"\": failed to verify receiver\n\n"},
		{name: "failed to close connection", smtp: &fakeTestSMTP{fail: map[string]bool{"quit": true, "close": true}},
			err: "problems with sending messages: 1 error occurred:\n\t* failed to close\n\n"},
		{name: "failed to make email writer", smtp: &fakeTestSMTP{fail: map[string]bool{"data": true}},
			err: "problems with sending messages: 1 error occurred:\n\t* can't send message to : can't make email writer: failed to send\n\n"},
	}
	e := Email{}
	for _, d := range testSet {
		t.Run(d.name, func(t *testing.T) {
			e.smtpClient = d.smtp
			assert.EqualError(t, e.sendBuffer(context.Background(), []emailMessage{{}}), d.err,
				"expected error for e.sendBuffer")
		})
	}
	e.smtpClient = nil
	assert.Error(t, e.sendBuffer(context.Background(), []emailMessage{{}}),
		"nil smtpClient passed to sendBuffer calls for e.client which in turns should return error")
	e.smtpClient = &fakeTestSMTP{}
	assert.NoError(t, e.sendBuffer(context.Background(), []emailMessage{{}}), "",
		"no error expected for e.sendBuffer in normal flow")
	e.smtpClient = &fakeTestSMTP{fail: map[string]bool{"quit": true}}
	assert.NoError(t, e.sendBuffer(context.Background(), []emailMessage{{}}), "",
		"no error expected for e.sendBuffer with	 failed smtpClient.Quit but successful smtpClient.Close")
	e.smtpClient = nil
	assert.EqualError(t, e.sendEmail(emailMessage{}), "sendEmail called without smtpClient set",
		"e.sendEmail called without smtpClient set returns error")
}

type fakeTestSMTP struct {
	fail map[string]bool

	buff       bytes.Buffer
	mail, rcpt string
	auth       bool
	close      bool
	quitCount  int
	lock       sync.RWMutex
}

func (f *fakeTestSMTP) Auth(smtp.Auth) error { f.auth = true; return nil }

func (f *fakeTestSMTP) Mail(m string) error {
	f.lock.Lock()
	f.mail = m
	f.lock.Unlock()
	if f.fail["mail"] {
		return errors.New("failed to verify sender")
	}
	return nil
}

func (f *fakeTestSMTP) Rcpt(r string) error {
	f.lock.Lock()
	f.rcpt = r
	f.lock.Unlock()
	if f.fail["rcpt"] {
		return errors.New("failed to verify receiver")
	}
	return nil
}

func (f *fakeTestSMTP) Quit() error {
	f.lock.Lock()
	f.quitCount++
	f.lock.Unlock()
	if f.fail["quit"] {
		return errors.New("failed to quit")
	}
	return nil
}

func (f *fakeTestSMTP) Close() error {
	f.close = true
	if f.fail["close"] {
		return errors.New("failed to close")
	}
	return nil
}

func (f *fakeTestSMTP) Data() (io.WriteCloser, error) {
	if f.fail["data"] {
		return nil, errors.New("failed to send")
	}
	return nopCloser{&f.buff}, nil
}

func (f *fakeTestSMTP) readRcpt() string {
	f.lock.RLock()
	defer f.lock.RUnlock()
	return f.rcpt
}

func (f *fakeTestSMTP) readMail() string {
	f.lock.RLock()
	defer f.lock.RUnlock()
	return f.mail
}

func (f *fakeTestSMTP) readQuitCount() int {
	f.lock.RLock()
	defer f.lock.RUnlock()
	return f.quitCount
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error {
	return nil
}
