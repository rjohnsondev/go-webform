package main

import (
	"fmt"
	"github.com/gorilla/sessions"
	"github.com/jcmturner/gokrb5/v8/keytab"
	"github.com/jcmturner/gokrb5/v8/service"
	"github.com/jcmturner/gokrb5/v8/spnego"
	"github.com/pkg/errors"
	"io/ioutil"
	"log"
	"net/http"
	"os"
)

type SessionMgr struct {
	skey       []byte
	store      sessions.Store
	cookieName string
}

func NewSessionMgr(sessionKey string, cookieName string) SessionMgr {
	// Best practice is to load this key from a secure location.
	skey := []byte(sessionKey)
	return SessionMgr{
		skey:       skey,
		store:      sessions.NewCookieStore(skey),
		cookieName: cookieName,
	}
}

func (smgr SessionMgr) Get(r *http.Request, k string) ([]byte, error) {
	s, err := smgr.store.Get(r, smgr.cookieName)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("nil session")
	}
	b, ok := s.Values[k].([]byte)
	if !ok {
		return nil, fmt.Errorf("could not get bytes held in session at %s", k)
	}
	return b, nil
}

func (smgr SessionMgr) New(w http.ResponseWriter, r *http.Request, k string, v []byte) error {
	s, err := smgr.store.New(r, smgr.cookieName)
	if err != nil {
		return fmt.Errorf("could not get new session from session manager: %v", err)
	}
	s.Values[k] = v
	return s.Save(r, w)
}

func (sm *spnegoMiddleware) Middleware(next http.Handler) http.Handler {
	l := log.New(os.Stderr, "GOKRB5 Service: ", log.Ldate|log.Ltime|log.Lshortfile)
	return spnego.SPNEGOKRB5Authenticate(
		next,
		sm.kt,
		service.Logger(l),
		service.SessionManager(NewSessionMgr(sm.sessionKey, sm.cookieName)),
	)
}

type spnegoMiddleware struct {
	kt         *keytab.Keytab
	sessionKey string
	cookieName string
}

func spnegoFromKeytab(keytabFilename string, sessionKey string, cookieName string) (*spnegoMiddleware, error) {
	b, err := ioutil.ReadFile(keytabFilename)
	if err != nil {
		return nil, err
	}
	kt := keytab.New()
	if kt.Unmarshal(b) != nil {
		return nil, err
	}

	sm := spnegoMiddleware{
		kt:         kt,
		sessionKey: sessionKey,
		cookieName: cookieName,
	}
	return &sm, nil
}
