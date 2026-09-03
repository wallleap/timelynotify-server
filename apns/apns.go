package apns

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/mritd/logger"
	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
	"golang.org/x/net/http2"
)

type PushMessage struct {
	Id          string `form:"id,omitempty" json:"id,omitempty" xml:"id,omitempty" query:"id,omitempty"`
	DeviceToken string `form:"-" json:"-" xml:"-" query:"-"`
	DeviceKey   string `form:"device_key,omitempty" json:"device_key,omitempty" xml:"device_key,omitempty" query:"device_key,omitempty"`
	Subtitle    string `form:"subtitle,omitempty" json:"subtitle,omitempty" xml:"subtitle,omitempty" query:"subtitle,omitempty"`
	Title       string `form:"title,omitempty" json:"title,omitempty" xml:"title,omitempty" query:"title,omitempty"`
	Body        string `form:"body,omitempty" json:"body,omitempty" xml:"body,omitempty" query:"body,omitempty"`
	// ios notification sound(system sound please refer to http://iphonedevwiki.net/index.php/AudioServices)
	Sound string `form:"sound,omitempty" json:"sound,omitempty" xml:"sound,omitempty" query:"sound,omitempty"`
	// Badge is the number displayed on the app icon.  Any int is accepted
	// (including zero to clear the badge).  HasBadge disambiguates the zero
	// value ("explicit 0" vs "parameter was not sent").
	Badge int `form:"badge,omitempty" json:"badge,omitempty" xml:"badge,omitempty" query:"badge,omitempty"`
	// HasBadge is true when the request explicitly provided a badge value
	// (including zero).  When false, Badge is ignored (APNs badge is left
	// as-is).  This field is internal and intentionally has no struct tags
	// so it is never serialized to the wire.
	HasBadge  bool
	ExtParams map[string]interface{} `form:"ext_params,omitempty" json:"ext_params,omitempty" xml:"ext_params,omitempty" query:"ext_params,omitempty"`
}

// Check if it's an empty message, empty messages might be silent push notifications
func (p PushMessage) IsEmptyAlert() bool {
	return p.Title == "" && p.Body == "" && p.Subtitle == ""
}

func (p PushMessage) IsDelete() bool {
	val := p.ExtParams["delete"]
	return val == "1" || val == 1 || val == 1.0
}

const (
	topic          = "me.fin.bark"
	keyID          = "LH4T9V5U4R"
	teamID         = "5U8LBRXG3A"
	PayloadMaximum = 4096
)

var clients = make(chan *apns2.Client, 1)

// Initialize APNS client pool
func init() {
	ReCreateAPNS(1)
}

func ReCreateAPNS(maxClientCount int) error {
	if maxClientCount < 1 {
		return fmt.Errorf("invalid number of clients")
	}

	authKey, err := token.AuthKeyFromBytes([]byte(apnsPrivateKey))
	if err != nil {
		logger.Fatalf("failed to create APNS auth key: %v", err)
	}

	var rootCAs *x509.CertPool
	if runtime.GOOS == "windows" {
		rootCAs = x509.NewCertPool()
	} else {
		rootCAs, err = x509.SystemCertPool()
		if err != nil {
			logger.Fatalf("failed to get rootCAs: %v", err)
		}
	}

	for _, ca := range apnsCAs {
		rootCAs.AppendCertsFromPEM([]byte(ca))
	}

	clients = make(chan *apns2.Client, maxClientCount)

	for i := 0; i < min(runtime.NumCPU(), maxClientCount); i++ {
		client := &apns2.Client{
			Token: &token.Token{
				AuthKey: authKey,
				KeyID:   keyID,
				TeamID:  teamID,
			},
			HTTPClient: &http.Client{
				Transport: &http2.Transport{
					DialTLS: apns2.DialTLS,
					TLSClientConfig: &tls.Config{
						RootCAs: rootCAs,
					},
				},
				Timeout: apns2.HTTPClientTimeout,
			},
			Host: apns2.HostProduction,
		}
		logger.Infof("create apns client: %d", i)
		clients <- client
	}

	logger.Info("init apns client success...")
	return nil
}

func Push(msg *PushMessage) (code int, err error) {
	pl := payload.NewPayload().MutableContent()
	pushType := apns2.PushTypeAlert
	if msg.IsDelete() {
		// Silent push notification
		pl = pl.ContentAvailable()
		pushType = apns2.PushTypeBackground
	} else {
		// Regular push notification
		pl = pl.AlertTitle(msg.Title).
			AlertSubtitle(msg.Subtitle).
			AlertBody(msg.Body).
			Sound(msg.Sound).
			Category("myNotificationCategory")
		if msg.HasBadge {
			pl = pl.Badge(msg.Badge)
		}
		group, exist := msg.ExtParams["group"]
		if exist && group != nil {
			// ExtParams values arrive as arbitrary JSON types; assert via
			// Sprint instead of a type assertion to avoid a panic (and skip
			// nil so APNs never receives a literal "<nil>").
			pl = pl.ThreadID(fmt.Sprint(group))
		}
	}

	for k, v := range msg.ExtParams {
		// Change all parameter names to lowercase to prevent inconsistent capitalization
		lowerKey := strings.ToLower(k)
		// `sound` is already carried by aps.sound (with the .caf suffix);
		// `soundduration` is a HarmonyOS V3-only field. Neither should be
		// duplicated as a top-level custom APNs payload key.
		if lowerKey == "sound" || lowerKey == "soundduration" {
			continue
		}
		pl.Custom(lowerKey, fmt.Sprintf("%v", v))
	}

	client := <-clients // grab a client from the pool
	clients <- client   // add the client back to the pool

	resp, err := client.Push(&apns2.Notification{
		CollapseID:  msg.Id,
		DeviceToken: msg.DeviceToken,
		Topic:       topic,
		Payload:     pl,
		Expiration:  time.Now().Add(24 * time.Hour),
		PushType:    pushType,
	})
	if err != nil {
		return 500, err
	}
	if resp.StatusCode != 200 {
		return resp.StatusCode, errors.New(resp.Reason)
	}
	return 200, nil
}
