// Package session contains protobuf types for sessions.
package session

import (
	context "context"
	"fmt"

	"github.com/golang/protobuf/ptypes"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pomerium/pomerium/internal/identity"
	"github.com/pomerium/pomerium/pkg/grpc/databroker"
)

// Delete deletes a session from the databroker.
func Delete(ctx context.Context, client databroker.DataBrokerServiceClient, sessionID string) error {
	any, _ := ptypes.MarshalAny(new(Session))
	_, err := client.Delete(ctx, &databroker.DeleteRequest{
		Type: any.GetTypeUrl(),
		Id:   sessionID,
	})
	return err
}

// Get gets a session from the databroker.
func Get(ctx context.Context, client databroker.DataBrokerServiceClient, sessionID string) (*Session, error) {
	any, _ := ptypes.MarshalAny(new(Session))

	res, err := client.Get(ctx, &databroker.GetRequest{
		Type: any.GetTypeUrl(),
		Id:   sessionID,
	})
	if err != nil {
		return nil, err
	}

	var s Session
	err = ptypes.UnmarshalAny(res.GetRecord().GetData(), &s)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling session from databroker: %w", err)
	}
	return &s, nil
}

// Set sets a session in the databroker.
func Set(ctx context.Context, client databroker.DataBrokerServiceClient, s *Session) (*databroker.SetResponse, error) {
	any, _ := anypb.New(s)
	res, err := client.Set(ctx, &databroker.SetRequest{
		Type: any.GetTypeUrl(),
		Id:   s.Id,
		Data: any,
	})
	return res, err
}

// AddClaims adds the flattened claims to the session.
func (x *Session) AddClaims(claims identity.FlattenedClaims) {
	if x.Claims == nil {
		x.Claims = make(map[string]*structpb.ListValue)
	}
	for k, svs := range claims.ToPB() {
		x.Claims[k] = svs
	}
}

// SetRawIDToken sets the raw id token.
func (x *Session) SetRawIDToken(rawIDToken string) {
	if x.IdToken == nil {
		x.IdToken = new(IDToken)
	}
	x.IdToken.Raw = rawIDToken
}

// GetIssuedAt returns the issued at timestamp for the id token.
func (x *Session) GetIssuedAt() *timestamppb.Timestamp {
	return x.GetIdToken().GetIssuedAt()
}
