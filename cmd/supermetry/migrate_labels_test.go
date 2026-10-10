package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/crypto"
	"supermetry/internal/privacy"
	"supermetry/internal/testpg"
)

func TestMigrateLabels(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	policy, err := privacy.NewPathPolicy([]string{"/pricing", "/docs"})
	if err != nil {
		t.Fatal(err)
	}
	oldBox := func(info string) *crypto.SecretBox {
		box, err := crypto.NewSecretBox(key, info)
		if err != nil {
			t.Fatal(err)
		}
		return box
	}
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,totp_secret_enc,totp_enabled) values('a@example.test','a','x',$1,true) returning id`,
		oldBox(legacyTOTPInfo).Seal([]byte("TOTPSECRET"))).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('o','o') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	channel, _ := json.Marshal(map[string]string{"url": b64(oldBox(legacyAlertsInfo).Seal([]byte("https://hook.test"))), "secret": b64(oldBox(legacyAlertsInfo).Seal([]byte("shh")))})
	if _, err := pool.Exec(ctx, `insert into notification_channels(org_id,kind,name,config) values($1,'webhook','w',$2)`, orgID, channel); err != nil {
		t.Fatal(err)
	}
	storage, _ := json.Marshal(map[string]any{"backend": "s3", "s3": map[string]string{"bucket": "b", "secret_key": b64(oldBox(legacyReplayS3Info).Seal([]byte("S3SECRET")))}})
	if _, err := pool.Exec(ctx, `update instance_settings set settings=jsonb_build_object('replay_storage',$1::jsonb)`, storage); err != nil {
		t.Fatal(err)
	}

	oldID := policy.FingerprintForLabel(legacyPathPolicyBase)
	if oldID == policy.Fingerprint() {
		t.Fatal("old and new fingerprints are equal")
	}
	if _, err := pool.Exec(ctx, `insert into verified_page_day(site_id,day,path_sanitizer_version,path_policy_id,path_id,visitors,visits,pageviews) values(1,$2,1,$1,0,1,1,1)`, oldID, day); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		report, err := migrateLabels(ctx, pool, key, policy)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		want := labelMigrationReport{TOTP: 1, Channels: 1, ReplayS3: 1, PathRows: 1}
		if run == 1 {
			want = labelMigrationReport{}
		}
		if report != want {
			t.Fatalf("run %d report = %+v, want %+v", run, report, want)
		}
	}

	var sealed []byte
	if err := pool.QueryRow(ctx, `select totp_secret_enc from users where id=$1`, userID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if plain, err := oldBox("supermetry/totp/v1").Open(sealed); err != nil || string(plain) != "TOTPSECRET" {
		t.Fatalf("TOTP after migration = %q, %v", plain, err)
	}
	var cfg map[string]string
	var raw []byte
	if err := pool.QueryRow(ctx, `select config from notification_channels`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &cfg)
	sealed, _ = base64.StdEncoding.DecodeString(cfg["secret"])
	if plain, err := oldBox("supermetry/alerts/v1").Open(sealed); err != nil || string(plain) != "shh" {
		t.Fatalf("channel secret after migration = %q, %v", plain, err)
	}
	var s3secret string
	if err := pool.QueryRow(ctx, `select settings->'replay_storage'->'s3'->>'secret_key' from instance_settings`).Scan(&s3secret); err != nil {
		t.Fatal(err)
	}
	sealed, _ = base64.StdEncoding.DecodeString(s3secret)
	if plain, err := oldBox("supermetry/replay-s3/v1").Open(sealed); err != nil || string(plain) != "S3SECRET" {
		t.Fatalf("replay S3 secret after migration = %q, %v", plain, err)
	}
	var id string
	if err := pool.QueryRow(ctx, `select path_policy_id from verified_page_day`).Scan(&id); err != nil || id != policy.Fingerprint() {
		t.Fatalf("path_policy_id = %q, %v", id, err)
	}

	if _, err := migrateLabels(ctx, pool, bytes.Repeat([]byte{9}, 32), policy); err == nil {
		t.Fatal("migration with the wrong secret key succeeded")
	}
}
