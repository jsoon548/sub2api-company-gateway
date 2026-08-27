package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
)

type authorizationFile struct {
	TargetUserID         int64     `json:"target_user_id"`
	DeploymentOperatorID string    `json:"deployment_operator_id"`
	Reason               string    `json:"reason"`
	Nonce                string    `json:"nonce"`
	ExpiresAt            time.Time `json:"expires_at"`
	SignatureHex         string    `json:"signature_hex"`
}

func main() {
	var authPath string
	flag.StringVar(&authPath, "authorization", "", "path to a one-time signed recovery authorization JSON")
	flag.Parse()
	if authPath == "" {
		fatal("--authorization is required")
	}
	dsn := os.Getenv("DATABASE_DSN")
	secret := []byte(os.Getenv("SUPER_ADMIN_RECOVERY_SECRET"))
	if dsn == "" || len(secret) < 32 {
		fatal("DATABASE_DSN and a 32+ byte SUPER_ADMIN_RECOVERY_SECRET are required")
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		fatal(err.Error())
	}
	var file authorizationFile
	if err := json.Unmarshal(raw, &file); err != nil {
		fatal(err.Error())
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fatal(err.Error())
	}
	defer db.Close()
	repo := repository.NewGovernanceRepository(db)
	governance := service.NewSuperAdminTransferService(repo, nil, nil)
	command := service.NewEmergencyRecoveryCommand(governance)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := command.Execute(ctx, service.EmergencyRecoveryAuthorization{
		TargetUserID: file.TargetUserID, DeploymentOperatorID: file.DeploymentOperatorID, Reason: file.Reason,
		Nonce: file.Nonce, ExpiresAt: file.ExpiresAt, SignatureHex: file.SignatureHex,
	}, secret)
	if err != nil {
		fatal(err.Error())
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
