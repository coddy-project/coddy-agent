package providerlogin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

var kimiStartForLogin = StartKimiLogin
var kimiListForLogin = llm.ListKimiModels

// EnsureKimiIdentity persists the managed identity before any device request.
func EnsureKimiIdentity(cfg *config.Config, prov config.ProviderConfig) (config.ProviderConfig, error) {
	if cfg == nil || prov.Type != "kimi" || (prov.Mode != "" && prov.Mode != "subscription") {
		return prov, errors.New("kimi: subscription provider required")
	}
	data, err := os.ReadFile(cfg.Paths.ConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return prov, err
	}
	if errors.Is(err, os.ErrNotExist) && prov.KimiIdentity != "" {
		return prov, errors.New("kimi: managed identity is not saved")
	}
	persisted := false
	if err == nil {
		current, loadErr := config.LoadWithPaths(cfg.Paths)
		if loadErr != nil {
			return prov, loadErr
		}
		existing := current.FindProvider(prov.Name)
		old := cfg.FindProvider(prov.Name)
		if (existing == nil) != (old == nil) || existing != nil && (existing.Type != old.Type || existing.Mode != old.Mode || existing.Region != old.Region || existing.KimiIdentity != old.KimiIdentity) {
			return prov, errors.New("kimi: provider changed since config was loaded")
		}
		if existing != nil {
			prov = *existing
			persisted = true
		}
	}
	if prov.KimiIdentity != "" {
		if !persisted {
			return prov, errors.New("kimi: managed identity is not saved")
		}
		if !config.ValidKimiIdentity(prov.KimiIdentity) {
			return prov, errors.New("kimi: invalid managed identity")
		}
		return prov, nil
	}
	prov.KimiIdentity, err = config.NewKimiIdentity()
	if err != nil {
		return prov, err
	}
	var cmd config.UCICommand
	if cfg.FindProvider(prov.Name) == nil {
		fields := map[string]string{"name": prov.Name, "type": "kimi", "kimi_identity": prov.KimiIdentity}
		if prov.Mode != "" {
			fields["mode"] = prov.Mode
		}
		if prov.Region != "" {
			fields["region"] = prov.Region
		}
		if prov.Proxy != "" {
			fields["proxy"] = prov.Proxy
		}
		value, marshalErr := json.Marshal(fields)
		if marshalErr != nil {
			return prov, marshalErr
		}
		cmd = config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("providers[name=%s]", prov.Name), Value: string(value)}
	} else {
		cmd = config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("providers[name=%s].kimi_identity", prov.Name), Value: prov.KimiIdentity}
	}
	result, err := config.CommitUCICommandsIfUnchanged(cfg.Paths, data, []config.UCICommand{cmd})
	if err != nil {
		return prov, err
	}
	if result.Config == nil || result.Config.FindProvider(prov.Name) == nil || result.Config.FindProvider(prov.Name).KimiIdentity != prov.KimiIdentity {
		return prov, errors.New("kimi: identity not present in persisted config")
	}
	return prov, nil
}

// KimiLogin holds a device login and its durable, single-use attempt.
type KimiLogin struct {
	Device    *llm.KimiDeviceLogin
	store     *llm.KimiCredentialStore
	region    string
	attempt   string
	waitToken func(context.Context) (llm.KimiToken, error)
}

func StartKimiLogin(ctx context.Context, home string, prov config.ProviderConfig) (*KimiLogin, error) {
	if prov.Type != "kimi" || (prov.Mode != "" && prov.Mode != "subscription") {
		return nil, errors.New("kimi: device login requires subscription mode")
	}
	region, err := llm.KimiCanonicalRegion(prov.Region)
	if err != nil {
		return nil, err
	}
	dir := config.KimiIdentityDir(home, prov.KimiIdentity)
	if dir == "" {
		return nil, errors.New("kimi: invalid credential identity")
	}
	store := llm.NewKimiCredentialStore(dir)
	attempt, err := store.BeginLogin(ctx, region)
	if err != nil {
		return nil, err
	}
	login := &KimiLogin{store: store, region: region, attempt: attempt}
	device, err := llm.StartKimiDeviceLogin(ctx, region, prov.Proxy)
	if err != nil {
		if cancelErr := login.Cancel(); cancelErr != nil {
			return nil, fmt.Errorf("kimi: device start failed and attempt cancellation failed: %w", cancelErr)
		}
		return nil, err
	}
	login.Device = device
	login.waitToken = device.Wait
	return login, nil
}

func (l *KimiLogin) Cancel() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := l.store.CancelLogin(ctx, l.region, l.attempt)
	if errors.Is(err, llm.ErrKimiLoginAttemptStale) {
		return nil
	}
	return err
}

func (l *KimiLogin) Wait(ctx context.Context) error {
	return l.WaitChecked(ctx, nil)
}

// WaitChecked runs check after polling and before committing the credential.
func (l *KimiLogin) WaitChecked(ctx context.Context, check func() error) error {
	token, err := l.waitToken(ctx)
	if err != nil {
		if cancelErr := l.Cancel(); cancelErr != nil {
			return fmt.Errorf("kimi: login failed and attempt cancellation failed: %w", cancelErr)
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		if cancelErr := l.Cancel(); cancelErr != nil {
			return cancelErr
		}
		return err
	}
	if check != nil {
		if err := check(); err != nil {
			if cancelErr := l.Cancel(); cancelErr != nil {
				return cancelErr
			}
			return err
		}
	}
	return l.store.CompleteLogin(ctx, l.region, l.attempt, llm.KimiCredential{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.ExpiresAt, ExpiresIn: token.ExpiresIn, DeviceID: token.DeviceID})
}

func runKimi(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, h Hooks, res *Result) error {
	row, err := EnsureKimiIdentity(cfg, *prov)
	if err != nil {
		return err
	}
	login, err := kimiStartForLogin(ctx, cfg.Paths.Home, row)
	if err != nil {
		return err
	}
	h.prompt(Prompt{URL: login.Device.URL, Code: login.Device.UserCode})
	if err := login.Wait(ctx); err != nil {
		return err
	}
	res.AuthPath = llm.NewKimiCredentialStore(config.KimiIdentityDir(cfg.Paths.Home, row.KimiIdentity)).CredentialPath(row.Region)
	res.Added, res.ConfigErr = ApplyKimiLoginCatalog(ctx, cfg, row)
	if res.ConfigErr == nil && (cfg.FindProvider(row.Name) == nil || cfg.FindProvider(row.Name).KimiIdentity != row.KimiIdentity) {
		res.Added = append(res.Added, "provider "+row.Name)
	}
	return nil
}
