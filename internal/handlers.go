package internal

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/dgrijalva/jwt-go"
	"github.com/pkg/errors"
	"github.com/stellar/go/network"
	"github.com/stellar/go/xdr"
	"github.com/thedevsaddam/govalidator"
	"net/http"
	"stellar-fi-anchor/internal/authentication"
	"stellar-fi-anchor/internal/stellar-sdk"
	"strings"
	"time"
)

type GetAuthResponse struct {
	Transaction string `json:"transaction"`
}

type AuthorizationService interface {
	BuildSignEncodeChallengeTransactionForAccount(id string) (string, error)
	ValidateClientSignedChallengeTransaction(
		txe *xdr.TransactionEnvelope) []error
}

func NewGetAuthHandler(authService AuthorizationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := r.URL.Query().Get("account")
		if accountID == "" {
			errorPayload := Payload{
				Error: map[string]interface{}{
					"message": "account is a required query parameter",
				},
			}
			w.WriteHeader(http.StatusBadRequest)
			err := json.NewEncoder(w).Encode(&errorPayload)
			if err != nil {
				panic(err)
			}
			return
		}

		transaction, err := authService.BuildSignEncodeChallengeTransactionForAccount(accountID)
		if err != nil {
			origErr := errors.Cause(err)
			switch origErr.(type) {
			case *stellarsdk.InvalidAccountID:
				errorPayload := Payload{
					Error: map[string]interface{}{
						"message": "account id is invalid",
					},
				}
				w.WriteHeader(http.StatusBadRequest)
				err := json.NewEncoder(w).Encode(&errorPayload)
				if err != nil {
					panic(err)
				}
				return
			default:
				panic(err)
			}
		}

		dataPayload := GetAuthResponse{Transaction: transaction}
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(&dataPayload)
		if err != nil {
			panic(err)
		}
	}
}

type transactionAuth struct {
	Transaction string `json:"transaction"`
}

type tokenPayload struct {
	Token string `json:"token"`
}

func NewPostAuthHandler(authService AuthorizationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rules := govalidator.MapData{
			"transaction": []string{"required"},
		}
		messages := govalidator.MapData{
			"transaction": []string{"required"},
		}

		body := transactionAuth{}
		opts := govalidator.Options{
			Request:         r,
			Data:            &body,
			Rules:           rules,
			Messages:        messages,
			RequiredDefault: true,
		}
		v := govalidator.New(opts)
		e := v.ValidateJSON()
		if len(e) > 0 {
			errorPayload := Payload{
				Error: map[string]interface{}{
					"message": "bad request",
					"errors":  e,
				},
			}
			w.WriteHeader(http.StatusBadRequest)
			err := json.NewEncoder(w).Encode(&errorPayload)
			if err != nil {
				panic(err)
			}
			return
		}

		rawr := strings.NewReader(body.Transaction)
		b64r := base64.NewDecoder(base64.StdEncoding, rawr)
		var txe xdr.TransactionEnvelope
		_, err := xdr.Unmarshal(b64r, &txe)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			errorPayload := Payload{
				Error: map[string]interface{}{
					"message": "the transaction cannot be decoded or parsed",
				},
			}
			err := json.NewEncoder(w).Encode(&errorPayload)
			if err != nil {
				panic(err)
			}
			return
		}

		validationErrs := authService.ValidateClientSignedChallengeTransaction(&txe)
		for _, e := range validationErrs {
			switch e.(type) {
			case *authentication.TransactionSourceAccountDoesntMatchAnchorPublicKey,
				*authentication.TransactionIsMissingTimeBounds,
				*authentication.TransactionChallengeExpired,
				*authentication.TransactionChallengeIsNotAManageDataOperation,
				*authentication.TransactionChallengeDoesNotHaveOnlyOneOperation,
				*authentication.TransactionOperationSourceAccountIsEmpty,
				*authentication.TransactionOperationsIsNil,
				*authentication.TransactionIsNotSignedByAnchor,
				*authentication.TransactionIsNotSignedByClient:

				continue
			default:
				panic(err)
			}
		}

		if len(validationErrs) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			errorPayload := Payload{
				Error: map[string]interface{}{
					"errors": validationErrs,
				},
			}
			err := json.NewEncoder(w).Encode(&errorPayload)
			if err != nil {
				panic(err)
			}
			return
		}

		now := time.Now()
		txeHash, err := network.HashTransaction(&txe.Tx, network.TestNetworkPassphrase)
		if err != nil {
			panic(err)
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"iss": "",
			"sub": txe.Tx.Operations[0].SourceAccount.Address(),
			"iat": now.Unix(),
			"exp": now.Add(24 * time.Hour).Unix(),
			"jti": hex.EncodeToString(txeHash[:]),
		})

		encodedToken, err := token.SignedString([]byte("SA4VF5RNXMFWS4JPLXDRP3D3SLSKMAZMCCXYC24LXMXUVYJLBN3F2ISY"))
		if err != nil {
			panic(err)
		}

		dataPayload := tokenPayload{Token: encodedToken}
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(&dataPayload)
		if err != nil {
			panic(err)
		}

	}
}