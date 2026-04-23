package service

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"

	"github.com/AmirSoleimani/openberth/apps/server/internal/runtime"
	"github.com/AmirSoleimani/openberth/apps/server/internal/framework"
	"github.com/AmirSoleimani/openberth/apps/server/internal/secret"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// SecretMeta is a re-export of store.SecretMeta for use in handlers.
type SecretMeta = store.SecretMeta

// SecretSetResult is the result of creating or updating a secret.
type SecretSetResult struct {
	Name      string   `json:"name"`
	Created   bool     `json:"created"`                // true if new, false if updated
	Restarted []string `json:"restarted,omitempty"`     // deployment names that were restarted
}

// SecretSet creates or updates an encrypted secret.
// If updating an existing secret, auto-restarts all deployments using it.
func (svc *Service) SecretSet(user *store.User, name, value, description string, global bool) (*SecretSetResult, error) {
	if name == "" {
		return nil, ErrBadRequest("Secret name is required.")
	}
	if value == "" {
		return nil, ErrBadRequest("Secret value is required.")
	}

	masterKey, err := svc.Cfg.GetMasterKeyBytes()
	if err != nil {
		return nil, ErrInternal("Encryption not configured: " + err.Error())
	}

	encDEK, dekNonce, ciphertext, valNonce, err := secret.Encrypt(masterKey, value)
	if err != nil {
		return nil, ErrInternal("Failed to encrypt secret: " + err.Error())
	}

	var userID *string
	if !global {
		userID = &user.ID
	}

	// Check if an existing row would collide. When targeting a global, look up
	// the global row directly — `GetSecret` prefers user-scoped over global
	// when both exist, which would hide the real collision.
	var existing *store.Secret
	if global {
		existing, _ = svc.Store.GetGlobalSecret(name)
		// Also check if a user-scoped secret with the same name exists —
		// that's a scope mismatch we need to reject below.
		if existing == nil {
			if userScoped, _ := svc.Store.GetSecret(user.ID, name); userScoped != nil && userScoped.UserID != nil {
				return nil, ErrBadRequest(fmt.Sprintf("Secret %q already exists as a user secret. Remove --global to update it.", name))
			}
		}
	} else {
		existing, _ = svc.Store.GetSecret(user.ID, name)
		if existing != nil && existing.UserID == nil {
			return nil, ErrBadRequest(fmt.Sprintf("Secret %q already exists as a global secret. Use --global to update it.", name))
		}
	}
	isUpdate := existing != nil

	// Creator/admin gate for globals. User-scoped secrets are implicitly
	// owned by the querying user (user_id scoped), so only globals need the
	// CreatedBy check.
	if isUpdate && global && !IsAdmin(user) {
		if existing.CreatedBy == nil || *existing.CreatedBy != user.ID {
			return nil, ErrForbidden("Only the creator of this global secret (or an admin) can update it.")
		}
	}

	if err := svc.Store.SetSecret(userID, scopeStr(global), name, description, user.ID, encDEK, dekNonce, ciphertext, valNonce); err != nil {
		return nil, ErrInternal("Failed to store secret: " + err.Error())
	}

	result := &SecretSetResult{
		Name:    name,
		Created: !isUpdate,
	}

	// Auto-restart affected deployments if this was an update
	if isUpdate {
		result.Restarted = svc.restartDeploymentsUsingSecret(name, user.Name)
	}

	return result, nil
}

func scopeStr(global bool) string {
	if global {
		return "global"
	}
	return "user"
}

// restartDeploymentsUsingSecret finds and recreates runtime containers for all
// deployments referencing a secret. Skips the build phase — only the runtime
// container is replaced with the new env vars.
func (svc *Service) restartDeploymentsUsingSecret(secretName, userName string) []string {
	deploys, err := svc.Store.GetDeploymentsUsingSecret(secretName)
	if err != nil || len(deploys) == 0 {
		return nil
	}

	var restarted []string
	for _, d := range deploys {
		if d.Status != "running" {
			continue
		}
		deploy := d
		go svc.recreateForSecretRotation(&deploy, userName)
		restarted = append(restarted, d.Name)
	}
	return restarted
}

// recreateForSecretRotation resolves secrets and recreates the runtime container
// without a full rebuild. Much faster than detectAndRebuild (~5s vs 30-60s).
func (svc *Service) recreateForSecretRotation(deploy *store.Deployment, userName string) {
	codeDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)

	fw := framework.DetectWithOverrides(codeDir)
	if fw == nil {
		log.Printf("[secret-rotate] Cannot detect framework for %s, skipping", deploy.ID)
		return
	}

	// Load user-supplied env
	userEnv := map[string]string{}
	if deploy.EnvJSON != "" && deploy.EnvJSON != "{}" {
		json.Unmarshal([]byte(deploy.EnvJSON), &userEnv)
	}

	// Resolve secrets fresh
	envVars := map[string]string{}
	secretNames := parseSecretsJSON(deploy.SecretsJSON)
	if len(secretNames) > 0 {
		secretEnv, err := svc.resolveSecrets(deploy.UserID, secretNames)
		if err != nil {
			log.Printf("[secret-rotate] Failed to resolve secrets for %s: %v", deploy.ID, err)
			return
		}
		for k, v := range secretEnv {
			envVars[k] = v
		}
	}
	for k, v := range userEnv {
		envVars[k] = v
	}

	port := resolvePort(0, fw.Port)

	result, err := svc.Runtime.RestartRuntime(runtime.DeployOpts{
		ID:           deploy.ID,
		UserID:       deploy.UserID,
		CodeDir:      codeDir,
		Framework:    fw.Framework,
		Language:     fw.Language,
		Port:         port,
		Image:        fw.Image,
		RunImage:     fw.RunImage,
		StartCmd:     fw.StartCmd,
		FrameworkEnv: fw.Env,
		UserEnv:      envVars,
		Memory:       svc.ResolveMemory(deploy.Memory),
		CPUs:         svc.ResolveCPUs(deploy.CPUs),
		NetworkQuota: deploy.NetworkQuota,
	})
	if err != nil {
		log.Printf("[secret-rotate] Restart failed for %s: %v", deploy.ID, err)
		svc.Store.UpdateDeploymentStatus(deploy.ID, "failed")
		return
	}
	svc.Store.UpdateDeploymentRunning(deploy.ID, result.InstanceID, result.Endpoint.Port)
	svc.Proxy.AddRoute(deploy.Subdomain, result.Endpoint.Port, AccessControlFromDeployment(deploy))
	log.Printf("[secret-rotate] %s restarted | user=%s", deploy.Subdomain, userName)
}

// SecretDelete removes a secret.
func (svc *Service) SecretDelete(user *store.User, name string, global bool) error {
	if name == "" {
		return ErrBadRequest("Secret name is required.")
	}

	// For global secrets, only the creator (or an admin) may delete.
	if global && !IsAdmin(user) {
		existing, _ := svc.Store.GetGlobalSecret(name)
		if existing == nil {
			return ErrNotFound("Secret not found.")
		}
		if existing.CreatedBy == nil || *existing.CreatedBy != user.ID {
			return ErrForbidden("Only the creator of this global secret (or an admin) can delete it.")
		}
	}

	var userID *string
	if !global {
		userID = &user.ID
	}
	return svc.Store.DeleteSecret(userID, name)
}

// SecretList returns metadata for user's secrets + all global secrets.
func (svc *Service) SecretList(user *store.User) ([]SecretMeta, error) {
	return svc.Store.ListSecrets(user.ID)
}

// resolveSecrets looks up secret names for a user, decrypts values, returns as env map.
// Returns error if any referenced secret name is not found.
func (svc *Service) resolveSecrets(userID string, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}

	masterKey, err := svc.Cfg.GetMasterKeyBytes()
	if err != nil {
		return nil, ErrInternal("Encryption not configured: " + err.Error())
	}

	secrets, err := svc.Store.GetSecretsByNames(userID, names)
	if err != nil {
		return nil, ErrInternal("Failed to fetch secrets: " + err.Error())
	}

	// Build a set of found names
	found := map[string]bool{}
	for _, s := range secrets {
		found[s.Name] = true
	}

	// Check all requested names exist
	for _, name := range names {
		if !found[name] {
			return nil, ErrBadRequest(fmt.Sprintf("Secret %q not found. Use 'berth secret list' to see available secrets.", name))
		}
	}

	result := make(map[string]string, len(secrets))
	for _, s := range secrets {
		plaintext, err := secret.Decrypt(masterKey, s.EncryptedDEK, s.DEKNonce, s.Ciphertext, s.ValueNonce)
		if err != nil {
			return nil, ErrInternal(fmt.Sprintf("Failed to decrypt secret %q: %v", s.Name, err))
		}
		result[s.Name] = plaintext
	}

	return result, nil
}

// mergeEnvAndSecrets resolves secrets and merges with explicit env vars.
// Returns two maps:
//
//   - buildEnv:   env visible to the build phase. Includes explicit env vars
//                 (set by the user via p.EnvVars) but *never* resolved secret
//                 values — a malicious postinstall script in a third-party
//                 npm / pip / go dependency would otherwise read production
//                 secrets during build.
//   - runtimeEnv: env visible to the runtime container. Full merge of secrets
//                 + explicit env vars; explicit vars win on key collision.
//
// The LegacyBuildSecrets config flag reverts to the old behavior
// (buildEnv == runtimeEnv) for one release so operators whose builds depend
// on a secret being present can migrate.
func (svc *Service) mergeEnvAndSecrets(userID string, env map[string]string, secretNames []string) (buildEnv, runtimeEnv map[string]string, err error) {
	env = ensureEnv(env)

	secretEnv, err := svc.resolveSecrets(userID, secretNames)
	if err != nil {
		return nil, nil, err
	}

	// Runtime: secrets first, then explicit env overrides.
	runtimeEnv = make(map[string]string, len(secretEnv)+len(env))
	for k, v := range secretEnv {
		runtimeEnv[k] = v
	}
	for k, v := range env {
		runtimeEnv[k] = v
	}

	if svc.Cfg.LegacyBuildSecrets {
		// Explicit opt-in: old behavior. Log once per call so operators see
		// the deprecation warning in their deploy logs.
		log.Printf("[secrets] legacy_build_secrets=true — injecting resolved secret values into build container (deprecated; will be removed)")
		buildEnv = runtimeEnv
		return buildEnv, runtimeEnv, nil
	}

	// Build: only explicit env vars, with secret-named keys dropped. This
	// protects even operators who deliberately set a user env var with the
	// same name as a secret from leaking the resolved value to build.
	buildEnv = make(map[string]string, len(env))
	secretKey := make(map[string]bool, len(secretEnv))
	for k := range secretEnv {
		secretKey[k] = true
	}
	for k, v := range env {
		if secretKey[k] {
			continue
		}
		buildEnv[k] = v
	}
	return buildEnv, runtimeEnv, nil
}

// marshalSecrets serializes secret names to JSON array string.
func marshalSecrets(names []string) string {
	if len(names) == 0 {
		return "[]"
	}
	b, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// parseSecretsJSON deserializes a secrets JSON array string into a slice.
func parseSecretsJSON(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(s), &names); err != nil {
		log.Printf("[secrets] Failed to parse secrets_json %q: %v", s, err)
		return nil
	}
	return names
}
