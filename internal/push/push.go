// Package push implementa notificaciones Web Push (VAPID) sobre las
// suscripciones guardadas, y la gestión de las claves del servidor.
package push

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"colombia-difunde/internal/store"
)

const (
	settingVAPIDPublic  = "vapid_public_key"
	settingVAPIDPrivate = "vapid_private_key"
)

// KeyProvider entrega las claves VAPID estables del servidor.
type KeyProvider struct {
	public, private string
	subject         string
}

// LoadKeys devuelve las claves VAPID. Si vienen por configuración (env) se
// usan; si no, se generan una vez y se persisten en la DB para que sean
// estables entre reinicios.
func LoadKeys(ctx context.Context, st store.Store, pubEnv, privEnv, subject string) (*KeyProvider, error) {
	if pubEnv != "" && privEnv != "" {
		return &KeyProvider{public: pubEnv, private: privEnv, subject: subject}, nil
	}
	pub, ok1, err := st.GetSetting(ctx, settingVAPIDPublic)
	if err != nil {
		return nil, err
	}
	priv, ok2, err := st.GetSetting(ctx, settingVAPIDPrivate)
	if err != nil {
		return nil, err
	}
	if ok1 && ok2 && pub != "" && priv != "" {
		return &KeyProvider{public: pub, private: priv, subject: subject}, nil
	}
	priv, pub, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return nil, fmt.Errorf("generar VAPID keys: %w", err)
	}
	if err := st.SetSetting(ctx, settingVAPIDPublic, pub); err != nil {
		return nil, err
	}
	if err := st.SetSetting(ctx, settingVAPIDPrivate, priv); err != nil {
		return nil, err
	}
	return &KeyProvider{public: pub, private: priv, subject: subject}, nil
}

// PublicKey es la clave pública (applicationServerKey) que se entrega al frontend.
func (k *KeyProvider) PublicKey() string { return k.public }

// Result describe el resultado del envío a una suscripción.
type Result struct {
	// Gone indica que la suscripción ya no existe (404/410) y debe borrarse.
	Gone bool
}

// Send envía un payload a una suscripción Web Push.
func (k *KeyProvider) Send(sub store.PushSubscription, payload []byte) (Result, error) {
	wsub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256DH,
			Auth:   sub.Auth,
		},
	}
	resp, err := webpush.SendNotification(payload, wsub, &webpush.Options{
		Subscriber:      k.subject,
		VAPIDPublicKey:  k.public,
		VAPIDPrivateKey: k.private,
		TTL:             120,
		HTTPClient:      &http.Client{Timeout: 12 * time.Second},
	})
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return Result{Gone: true}, nil
	case http.StatusCreated, http.StatusOK, http.StatusNoContent:
		return Result{}, nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest:
		slog.Warn("push rechazado por el servicio", "status", resp.StatusCode, "endpoint", redact(sub.Endpoint))
		return Result{}, nil
	default:
		return Result{}, fmt.Errorf("push service HTTP %d", resp.StatusCode)
	}
}

// redact oculta el cuerpo de un endpoint en los logs (solo deja el host).
func redact(endpoint string) string {
	if i := len(endpoint); i > 0 {
		for j := 0; j < i; j++ {
			if endpoint[j] == '/' && j > 8 {
				return endpoint[:j]
			}
		}
	}
	return endpoint
}
