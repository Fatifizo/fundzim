package app

// Stage 18 (ADR-050): application keys from the secret manager. With FIELD_ENCRYPTION_PROVIDER=secretstore every key
// variable (and the worker's AUDIT_CHECKPOINT_KEY) holds a secretref:// reference; they are resolved once at startup,
// before any service is built, and a key that cannot be resolved stops the process (no fallback to an environment value
// or a default). Errors name variables, never values or references.

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/secretstore"
)

func keyStore(s config.Secrets) (*secretstore.KV2, error) {
	kv, err := secretstore.New(secretstore.Config{Addr: s.KV2Addr, Mount: s.KV2Mount, Token: s.KV2Token.Reveal(), RoleID: s.KV2RoleID,
		SecretID: s.KV2SecretID.Reveal(), CACert: s.KV2CAFile, CacheTTL: s.KV2CacheTTL, Timeout: s.KV2Timeout})
	if err != nil {
		return nil, fmt.Errorf("application keys: secret manager: %w", err)
	}
	return kv, nil
}

// resolveManagedKeys returns cfg with its key references resolved (unchanged for FIELD_ENCRYPTION_PROVIDER=local).
func resolveManagedKeys(ctx context.Context, cfg config.Config) (config.Config, error) {
	if !cfg.Security.Managed {
		return cfg, nil
	}
	kv, err := keyStore(cfg.Secrets)
	if err != nil {
		return cfg, err
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cfg.Security.ResolveManaged(rctx, kv.Resolve); err != nil {
		return cfg, fmt.Errorf("application keys: %w", err)
	}
	return cfg, nil
}

// resolveWorkerKeys resolves the application keys and the worker's audit checkpoint key reference.
func resolveWorkerKeys(ctx context.Context, cfg config.Config, wcfg WorkerConfig) (config.Config, WorkerConfig, error) {
	if !cfg.Security.Managed {
		return cfg, wcfg, nil
	}
	resolved, err := resolveManagedKeys(ctx, cfg)
	if err != nil {
		return cfg, wcfg, err
	}
	kv, err := keyStore(cfg.Secrets)
	if err != nil {
		return cfg, wcfg, err
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	v, err := kv.Resolve(rctx, wcfg.AuditCheckpointKey.Reveal())
	if err != nil {
		return cfg, wcfg, errors.New("audit checkpoint key: the secret manager did not return AUDIT_CHECKPOINT_KEY")
	}
	wcfg.AuditCheckpointKey = config.Secret(strings.TrimSpace(string(v)))
	if problem := checkpointKeyProblem(wcfg.AuditCheckpointKey.Reveal(), resolvedKeys(resolved.Security)); problem != "" {
		return cfg, wcfg, &config.ValidationError{Problems: []string{problem}}
	}
	return resolved, wcfg, nil
}

// resolvedKeys lists the application keys by variable name (for the one-key-per-purpose check).
func resolvedKeys(s config.Security) map[string]string {
	return map[string]string{"BLIND_INDEX_KEY": s.BlindIndexKey.Reveal(), "FIELD_ENCRYPTION_LOCAL_KEY": s.FieldEncryptionKey.Reveal(),
		"KYC_BLIND_INDEX_KEY": s.KYCBlindIndexKey.Reveal(), "KYC_FIELD_ENCRYPTION_LOCAL_KEY": s.KYCFieldKey.Reveal(),
		"COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY": s.ComplianceFieldKey.Reveal(), "DOCUMENT_TICKET_KEY": s.DocumentTicketKey.Reveal(),
		"STORAGE_SSE_C_KEY": s.StorageSSEKey.Reveal()}
}

// checkpointKeyProblem returns "" when key is 32 bytes of hex and differs from every other key.
func checkpointKeyProblem(key string, others map[string]string) string {
	if k, err := hex.DecodeString(key); err != nil || len(k) != 32 {
		return "AUDIT_CHECKPOINT_KEY is required: 32 bytes of hex (scripts/dev-env-init.sh generates one)"
	}
	names := make([]string, 0, len(others))
	for n := range others {
		names = append(names, n)
	}
	for _, n := range sortedStrings(names) {
		if v := others[n]; v != "" && strings.EqualFold(v, key) {
			return "AUDIT_CHECKPOINT_KEY must differ from " + n
		}
	}
	return ""
}
