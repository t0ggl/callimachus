package downloaders

import (
	"context"
	"strings"
	"testing"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/testutil"
)

func setupTestBlocklistDB(t *testing.T) (*BlocklistManager, *database.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	return NewBlocklistManager(db), db
}

func TestBlocklistManager_AddAndCheckByHash(t *testing.T) {
	t.Parallel()
	bm, _ := setupTestBlocklistDB(t)
	ctx := context.Background()

	upperHash := "A1B2C3D4E5F60123456789ABCDEF0123456789AB"
	lowerHash := strings.ToLower(upperHash)

	// Add entry with uppercase info-hash
	err := bm.Add(ctx, upperHash, "Synthetic Author - Synthetic Book Alpha", "corrupt audio stream")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 1. Check with lowercase hash
	isBlocked, reason, err := bm.IsBlocklisted(ctx, lowerHash, "")
	if err != nil {
		t.Fatalf("IsBlocklisted error: %v", err)
	}
	if !isBlocked {
		t.Errorf("expected isBlocked=true for lowercase hash query")
	}
	if reason != "corrupt audio stream" {
		t.Errorf("expected reason %q, got %q", "corrupt audio stream", reason)
	}

	// 2. Check with uppercase hash
	isBlocked, reason, err = bm.IsBlocklisted(ctx, upperHash, "")
	if err != nil {
		t.Fatalf("IsBlocklisted error: %v", err)
	}
	if !isBlocked {
		t.Errorf("expected isBlocked=true for uppercase hash query")
	}
	if reason != "corrupt audio stream" {
		t.Errorf("expected reason %q, got %q", "corrupt audio stream", reason)
	}

	// 3. Check with magnet URI containing the hash
	magnetURI := "magnet:?xt=urn:btih:" + upperHash + "&dn=Synthetic+Book+Alpha"
	isBlocked, _, err = bm.IsBlocklisted(ctx, magnetURI, "")
	if err != nil {
		t.Fatalf("IsBlocklisted magnet error: %v", err)
	}
	if !isBlocked {
		t.Errorf("expected isBlocked=true for magnet URI query")
	}

	// 4. Check an unrelated info-hash
	isBlocked, _, err = bm.IsBlocklisted(ctx, "ffffffffffffffffffffffffffffffffffffffff", "")
	if err != nil {
		t.Fatalf("IsBlocklisted error: %v", err)
	}
	if isBlocked {
		t.Errorf("expected isBlocked=false for unrelated info-hash")
	}
}

func TestBlocklistManager_CheckByTitle(t *testing.T) {
	t.Parallel()
	bm, _ := setupTestBlocklistDB(t)
	ctx := context.Background()

	// Add entry by title without info-hash
	err := bm.Add(ctx, "", "Synthetic Author - Synthetic Book Beta", "damaged epub zip archive")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 1. Exact title match
	isBlocked, reason, err := bm.IsBlocklisted(ctx, "", "Synthetic Author - Synthetic Book Beta")
	if err != nil {
		t.Fatalf("IsBlocklisted error: %v", err)
	}
	if !isBlocked {
		t.Errorf("expected isBlocked=true for exact title match")
	}
	if reason != "damaged epub zip archive" {
		t.Errorf("expected reason %q, got %q", "damaged epub zip archive", reason)
	}

	// 2. Raw scene-formatted release title match
	sceneTitle := "Synthetic.Author.Synthetic.Book.Beta.2023.EPUB-GROUP"
	isBlocked, _, err = bm.IsBlocklisted(ctx, "", sceneTitle)
	if err != nil {
		t.Fatalf("IsBlocklisted sceneTitle error: %v", err)
	}
	if !isBlocked {
		t.Errorf("expected isBlocked=true for scene title match")
	}

	// 3. Case variation
	upperTitle := "SYNTHETIC AUTHOR - SYNTHETIC BOOK BETA"
	isBlocked, _, err = bm.IsBlocklisted(ctx, "", upperTitle)
	if err != nil {
		t.Fatalf("IsBlocklisted upperTitle error: %v", err)
	}
	if !isBlocked {
		t.Errorf("expected isBlocked=true for uppercase title match")
	}

	// 4. Unrelated title returns false
	isBlocked, _, err = bm.IsBlocklisted(ctx, "", "Synthetic Author - Different Novel")
	if err != nil {
		t.Fatalf("IsBlocklisted error: %v", err)
	}
	if isBlocked {
		t.Errorf("expected isBlocked=false for unrelated title")
	}
}

func TestBlocklistManager_IdempotentInsert(t *testing.T) {
	t.Parallel()
	bm, _ := setupTestBlocklistDB(t)
	ctx := context.Background()

	hash := "1234567890abcdef1234567890abcdef12345678"

	// First insert
	err := bm.Add(ctx, hash, "Synthetic Title One", "first reason")
	if err != nil {
		t.Fatalf("first Add failed: %v", err)
	}

	// Duplicate insert with updated reason
	err = bm.Add(ctx, hash, "Synthetic Title One Updated", "second reason updated")
	if err != nil {
		t.Fatalf("duplicate Add by hash failed: %v", err)
	}

	isBlocked, reason, err := bm.IsBlocklisted(ctx, hash, "")
	if err != nil {
		t.Fatalf("IsBlocklisted error: %v", err)
	}
	if !isBlocked {
		t.Errorf("expected isBlocked=true after duplicate insert")
	}
	if reason != "second reason updated" {
		t.Errorf("expected updated reason, got %q", reason)
	}

	// Duplicate insert by title without hash
	err = bm.Add(ctx, "", "Synthetic Novel Gamma", "initial reason")
	if err != nil {
		t.Fatalf("first title Add failed: %v", err)
	}
	err = bm.Add(ctx, "", "Synthetic Novel Gamma", "new updated reason")
	if err != nil {
		t.Fatalf("duplicate title Add failed: %v", err)
	}

	isBlocked, reason, err = bm.IsBlocklisted(ctx, "", "Synthetic Novel Gamma")
	if err != nil {
		t.Fatalf("IsBlocklisted error: %v", err)
	}
	if !isBlocked || reason != "new updated reason" {
		t.Errorf("expected reason %q, got %q (isBlocked=%v)", "new updated reason", reason, isBlocked)
	}
}

func TestBlocklistManager_RemoveAndList(t *testing.T) {
	t.Parallel()
	bm, _ := setupTestBlocklistDB(t)
	ctx := context.Background()

	// Seed 5 blocklist items
	hashes := []string{
		"1111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333",
		"4444444444444444444444444444444444444444",
		"5555555555555555555555555555555555555555",
	}

	for i, h := range hashes {
		err := bm.Add(ctx, h, "Synthetic Book Item", "test reason")
		if err != nil {
			t.Fatalf("failed to seed item %d: %v", i, err)
		}
	}

	// Test pagination: limit 2, offset 0
	page1, err := bm.List(ctx, 2, 0)
	if err != nil {
		t.Fatalf("List page 1 failed: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("expected 2 items on page 1, got %d", len(page1))
	}

	// Test pagination: limit 2, offset 2
	page2, err := bm.List(ctx, 2, 2)
	if err != nil {
		t.Fatalf("List page 2 failed: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("expected 2 items on page 2, got %d", len(page2))
	}

	// Test pagination: limit 2, offset 4
	page3, err := bm.List(ctx, 2, 4)
	if err != nil {
		t.Fatalf("List page 3 failed: %v", err)
	}
	if len(page3) != 1 {
		t.Errorf("expected 1 item on page 3, got %d", len(page3))
	}

	// Test remove: delete the item on page 3
	itemToRemove := page3[0]
	err = bm.Remove(ctx, itemToRemove.ID)
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify the removed item is no longer blocklisted
	isBlocked, _, err := bm.IsBlocklisted(ctx, itemToRemove.InfoHash, "")
	if err != nil {
		t.Fatalf("IsBlocklisted check after remove failed: %v", err)
	}
	if isBlocked {
		t.Errorf("expected item %d to no longer be blocklisted", itemToRemove.ID)
	}

	// Verify total count is now 4
	allRemaining, err := bm.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(allRemaining) != 4 {
		t.Errorf("expected 4 remaining items, got %d", len(allRemaining))
	}
}

func TestShouldDownload_Pipeline(t *testing.T) {
	t.Parallel()
	bm, _ := setupTestBlocklistDB(t)
	ctx := context.Background()

	profile := QualityProfile{
		AllowedLanguages: []string{"en"},
		AllowedFormats:   []string{"epub", "m4b"},
	}

	// 1. Blocklisted release by hash
	blockedHash := "abababababababababababababababababababab"
	_ = bm.Add(ctx, blockedHash, "Synthetic Bad Hash Release", "corrupt container")

	cand1 := ReleaseCandidate{
		Title:       "Synthetic Good Title",
		InfoHash:    blockedHash,
		Format:      "epub",
		BitrateKbps: 0,
		Language:    "en",
	}
	shouldDl, reason, err := ShouldDownload(ctx, profile, bm, cand1)
	if err != nil {
		t.Fatalf("ShouldDownload failed: %v", err)
	}
	if shouldDl {
		t.Errorf("expected shouldDl=false for blocklisted hash, reason: %s", reason)
	}

	// 2. Blocklisted release by title
	_ = bm.Add(ctx, "", "Synthetic Poison Release", "malformed audio transcode")
	cand2 := ReleaseCandidate{
		Title:       "Synthetic Poison Release",
		Format:      "m4b",
		BitrateKbps: 128,
		Language:    "en",
	}
	shouldDl, reason, err = ShouldDownload(ctx, profile, bm, cand2)
	if err != nil {
		t.Fatalf("ShouldDownload failed: %v", err)
	}
	if shouldDl {
		t.Errorf("expected shouldDl=false for blocklisted title, reason: %s", reason)
	}

	// 3. Rejected by quality profile (foreign language)
	cand3 := ReleaseCandidate{
		Title:  "Synthetic Title [FR]",
		Format: "epub",
	}
	shouldDl, reason, err = ShouldDownload(ctx, profile, bm, cand3)
	if err != nil {
		t.Fatalf("ShouldDownload failed: %v", err)
	}
	if shouldDl {
		t.Errorf("expected shouldDl=false for foreign language [FR], reason: %s", reason)
	}

	// 4. Existing M4B is accepted regardless of bitrate for passthrough
	cand4 := ReleaseCandidate{
		Title:       "Synthetic Audio Release",
		Format:      "m4b",
		BitrateKbps: 32,
		Language:    "en",
	}
	shouldDl, reason, err = ShouldDownload(ctx, profile, bm, cand4)
	if err != nil {
		t.Fatalf("ShouldDownload failed: %v", err)
	}
	if !shouldDl {
		t.Errorf("expected shouldDl=true for M4B passthrough, reason: %s", reason)
	}

	// 5. Rejected by quality profile (unsupported format)
	cand5 := ReleaseCandidate{
		Title:  "Synthetic Executable.exe",
		Format: "exe",
	}
	shouldDl, reason, err = ShouldDownload(ctx, profile, bm, cand5)
	if err != nil {
		t.Fatalf("ShouldDownload failed: %v", err)
	}
	if shouldDl {
		t.Errorf("expected shouldDl=false for .exe format, reason: %s", reason)
	}

	// 6. Valid release passes pipeline
	cand6 := ReleaseCandidate{
		Title:       "Synthetic Pristine Release",
		Format:      "epub",
		BitrateKbps: 0,
		Language:    "en",
	}
	shouldDl, reason, err = ShouldDownload(ctx, profile, bm, cand6)
	if err != nil {
		t.Fatalf("ShouldDownload failed: %v", err)
	}
	if !shouldDl {
		t.Errorf("expected shouldDl=true for valid release, rejected with reason: %s", reason)
	}
}
