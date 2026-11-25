package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"dbcraft/internal/database"
)

const (
	passwordSession = "Until app restarts"
	passwordForever = "Forever"
	passwordNever   = "Do not save"
)

type connectionCredential struct {
	User, Password, Mode string
}

type credentialChange struct {
	key        string
	credential connectionCredential
}

func credentialKey(name string, cfg database.Config) string {
	cfg.Password, cfg.User = "", ""
	cfg.AdditionalProjects = nil
	cfg.HiddenProjects = nil
	cfg.ExplorerHiddenProjects, cfg.ExplorerHiddenSchemas = nil, nil
	data, _ := json.Marshal(struct {
		Name   string
		Config database.Config
	}{name, cfg})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func credentialPath() (string, error) {
	path, err := connectionPath()
	return filepath.Join(filepath.Dir(path), "credentials.json"), err
}

func readCredentialBlobs(path string) (map[string][]byte, error) {
	blobs := map[string][]byte{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return blobs, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &blobs)
	if blobs == nil {
		blobs = map[string][]byte{}
	}
	return blobs, err
}

func readCredentials(path string) (map[string]connectionCredential, error) {
	blobs, err := readCredentialBlobs(path)
	if err != nil {
		return nil, err
	}
	credentials := map[string]connectionCredential{}
	for key, blob := range blobs {
		data, decryptErr := unprotectCredential(blob)
		if decryptErr != nil {
			continue
		} // Credentials from another OS account require a new login.
		var credential connectionCredential
		if json.Unmarshal(data, &credential) == nil {
			credential.Mode = passwordForever
			credentials[key] = credential
		}
	}
	return credentials, nil
}

// This runs only on workers. Session credentials are never serialized.
func writeCredentials(path string, changes []credentialChange) error {
	if len(changes) == 0 {
		return nil
	}
	blobs, err := readCredentialBlobs(path)
	if err != nil {
		return err
	}
	for _, change := range changes {
		delete(blobs, change.key)
		if change.credential.Mode == passwordForever {
			data, err := json.Marshal(change.credential)
			if err != nil {
				return err
			}
			blob, err := protectCredential(data)
			if err != nil {
				return err
			}
			blobs[change.key] = blob
		}
	}
	data, err := json.Marshal(blobs)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "credentials-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (a *App) applyCredentials(changes []credentialChange) {
	w := a.ensureWorkspace()
	if w.credentials == nil {
		w.credentials = map[string]connectionCredential{}
	}
	for _, change := range changes {
		delete(w.credentials, change.key)
		if change.credential.Mode != passwordNever {
			w.credentials[change.key] = change.credential
		}
	}
}

func (a *App) credential(name string, cfg database.Config) (connectionCredential, bool) {
	credential, ok := a.ensureWorkspace().credentials[credentialKey(name, cfg)]
	return credential, ok
}
