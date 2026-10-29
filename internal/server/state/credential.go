package state

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"uuid"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
)

var (
	ErrCredentialNotFound    = errors.New("GitHub credential not found")
	ErrCredentialConflict    = errors.New("GitHub credential generation conflict")
	ErrCredentialUnavailable = errors.New("GitHub credential is not available")
)

type Credential struct {
	ViewerID   string
	Generation string
	Token      github.Token
}

func (Credential) String() string   { return "[redacted GitHub credential]" }
func (Credential) GoString() string { return "[redacted GitHub credential]" }

func (store *Store) PutCredential(ctx context.Context, userKey, viewerID, expectedGeneration string, token github.Token) (string, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return "", admissionError
	}
	defer releaseWriter()
	if err := validateIdentifier(userKey); err != nil {
		return "", err
	}
	if err := validateIdentifier(viewerID); err != nil {
		return "", err
	}
	generation := uuid.New().String()
	err := token.WithStorageMaterial(ctx, func(material []byte) error {
		aad, err := credentialAAD(userKey, viewerID, token.ClientID(), store.state.keyID, generation)
		if err != nil {
			return err
		}
		ciphertext := append([]byte{1}, store.state.aead.Seal(nil, nil, material, aad)...)
		tx, err := store.state.client.Tx(ctx)
		if err != nil {
			return fmt.Errorf("start GitHub credential transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		owner, err := findUser(ctx, tx.Client(), userKey)
		if err != nil {
			return err
		}
		existing, err := tx.GitHubCredential.Query().Where(githubcredential.UserIDEQ(owner.ID)).Only(ctx)
		if ent.IsNotFound(err) {
			if expectedGeneration != "" {
				return ErrCredentialConflict
			}
			if err := tx.GitHubCredential.Create().SetUserID(owner.ID).SetViewerID(viewerID).SetClientID(token.ClientID()).SetKeyID(store.state.keyID).SetGeneration(generation).SetCiphertext(ciphertext).Exec(ctx); err != nil {
				return fmt.Errorf("create GitHub credential: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("inspect GitHub credential: %w", err)
		} else {
			if expectedGeneration == "" || existing.Generation != expectedGeneration || existing.Status != githubcredential.StatusReady {
				return ErrCredentialConflict
			}
			if _, err := store.decryptCredential(userKey, existing); err != nil {
				return err
			}
			count, err := tx.GitHubCredential.Update().Where(githubcredential.IDEQ(existing.ID), githubcredential.GenerationEQ(expectedGeneration), githubcredential.StatusEQ(githubcredential.StatusReady)).SetViewerID(viewerID).SetClientID(token.ClientID()).SetKeyID(store.state.keyID).SetGeneration(generation).SetCiphertext(ciphertext).Save(ctx)
			if err != nil {
				return fmt.Errorf("update GitHub credential: %w", err)
			}
			if count != 1 {
				return ErrCredentialConflict
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("GitHub credential commit outcome may be uncertain: %w", err)
		}
		return nil
	})
	if err != nil {
		return generation, err
	}
	return generation, nil
}

func (store *Store) Credential(ctx context.Context, userKey string) (Credential, error) {
	if err := validateIdentifier(userKey); err != nil {
		return Credential{}, err
	}
	owner, err := findUser(ctx, store.state.client, userKey)
	if err != nil {
		return Credential{}, err
	}
	entry, err := store.state.client.GitHubCredential.Query().Where(githubcredential.UserIDEQ(owner.ID)).Only(ctx)
	if ent.IsNotFound(err) {
		return Credential{}, ErrCredentialNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("read GitHub credential: %w", err)
	}
	if entry.Status != githubcredential.StatusReady {
		return Credential{}, ErrCredentialUnavailable
	}
	token, err := store.decryptCredential(userKey, entry)
	if err != nil {
		return Credential{}, err
	}
	return Credential{ViewerID: entry.ViewerID, Generation: entry.Generation, Token: token.AccessOnly()}, nil
}

func (store *Store) decryptCredential(userKey string, entry *ent.GitHubCredential) (github.Token, error) {
	if entry.KeyID != store.state.keyID {
		return github.Token{}, fmt.Errorf("GitHub credential encryption key is unavailable")
	}
	aad, err := credentialAAD(userKey, entry.ViewerID, entry.ClientID, entry.KeyID, entry.Generation)
	if err != nil {
		return github.Token{}, err
	}
	if len(entry.Ciphertext) == 0 || entry.Ciphertext[0] != 1 {
		return github.Token{}, fmt.Errorf("invalid encrypted GitHub credential")
	}
	material, err := store.state.aead.Open(nil, nil, entry.Ciphertext[1:], aad)
	if err != nil {
		return github.Token{}, fmt.Errorf("decrypt GitHub credential failed")
	}
	defer clear(material)
	token, err := github.DecodeStoredToken(material)
	if err != nil {
		return github.Token{}, err
	}
	if token.ClientID() != entry.ClientID {
		return github.Token{}, fmt.Errorf("GitHub credential client identity is inconsistent")
	}
	return token, nil
}

func credentialAAD(userKey, viewerID, clientID, keyID, generation string) ([]byte, error) {
	data, err := json.Marshal(struct {
		Purpose    string `json:"purpose"`
		Version    int    `json:"version"`
		User       string `json:"user"`
		Viewer     string `json:"viewer"`
		Client     string `json:"client"`
		Key        string `json:"key"`
		Generation string `json:"generation"`
	}{Purpose: "skillhub.github-credential", Version: 1, User: userKey, Viewer: viewerID, Client: clientID, Key: keyID, Generation: generation})
	if err != nil {
		return nil, fmt.Errorf("encode GitHub credential identity")
	}
	return data, nil
}
