package downloaders_test

import (
	"net/http"
	"testing"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/downloaders"
)

func TestFactory_NewClientFromConfig(t *testing.T) {
	t.Parallel()

	user := "user1"
	pass := "pass1"
	token := "token1"

	// 1. qBittorrent
	qbtCfg := &database.DownloadClient{
		ID:       "dc-qbt-1",
		Name:     "qBittorrent",
		Type:     "qbittorrent",
		Host:     "127.0.0.1",
		Port:     8080,
		Username: &user,
		Password: &pass,
	}
	client1, err := downloaders.NewClientFromConfig(qbtCfg, http.DefaultClient)
	if err != nil {
		t.Fatalf("factory qbittorrent failed: %v", err)
	}
	if client1.Type() != downloaders.TypeQBittorrent {
		t.Errorf("expected type %s, got %s", downloaders.TypeQBittorrent, client1.Type())
	}

	// 2. Transmission
	trCfg := &database.DownloadClient{
		ID:       "dc-tr-1",
		Name:     "Transmission",
		Type:     "transmission",
		Host:     "127.0.0.1",
		Port:     9091,
		Username: &user,
		Password: &pass,
	}
	client2, err := downloaders.NewClientFromConfig(trCfg, http.DefaultClient)
	if err != nil {
		t.Fatalf("factory transmission failed: %v", err)
	}
	if client2.Type() != downloaders.TypeTransmission {
		t.Errorf("expected type %s, got %s", downloaders.TypeTransmission, client2.Type())
	}

	// 3. SABnzbd
	sabCfg := &database.DownloadClient{
		ID:       "dc-sab-1",
		Name:     "SABnzbd",
		Type:     "sabnzbd",
		Host:     "127.0.0.1",
		Port:     8080,
		APIToken: &token,
	}
	client3, err := downloaders.NewClientFromConfig(sabCfg, http.DefaultClient)
	if err != nil {
		t.Fatalf("factory sabnzbd failed: %v", err)
	}
	if client3.Type() != downloaders.TypeSABnzbd {
		t.Errorf("expected type %s, got %s", downloaders.TypeSABnzbd, client3.Type())
	}

	// 4. Unsupported
	badCfg := &database.DownloadClient{
		ID:   "dc-bad-1",
		Type: "deluge",
	}
	_, err = downloaders.NewClientFromConfig(badCfg, http.DefaultClient)
	if err == nil {
		t.Errorf("expected error for unsupported client type, got nil")
	}

	// 5. Nil config
	_, err = downloaders.NewClientFromConfig(nil, http.DefaultClient)
	if err == nil {
		t.Errorf("expected error for nil config, got nil")
	}
}
