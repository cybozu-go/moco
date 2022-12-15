package dbop

import (
	"context"
	"errors"
)

// ErrNop is a sentinel error for NopOperator
var ErrNop = errors.New("nop")

// NopOperator is an implementation of Operator that always returns ErrNop.
type NopOperator struct {
	name string
}

var _ Operator = NopOperator{}

func (o NopOperator) Name() string {
	return o.name
}

func (o NopOperator) Close() error {
	return nil
}

func (o NopOperator) GetStatus(context.Context) (*MySQLInstanceStatus, error) {
	return nil, ErrNop
}

func (o NopOperator) SubtractGTID(ctx context.Context, set1, set2 string) (string, error) {
	return "", ErrNop
}

func (o NopOperator) IsSubsetGTID(ctx context.Context, set1, set2 string) (bool, error) {
	return false, ErrNop
}

func (o NopOperator) FindTopRunner(context.Context, []*MySQLInstanceStatus) (int, error) {
	return 0, ErrNop
}

func (o NopOperator) ConfigureReplica(ctx context.Context, source AccessInfo, semisync bool) error {
	return ErrNop
}

func (o NopOperator) ConfigurePrimary(ctx context.Context, waitForCount int) error {
	return ErrNop
}

func (o NopOperator) StopReplicaIOThread(context.Context) error {
	return ErrNop
}

func (o NopOperator) WaitForGTID(ctx context.Context, gtidSet string, timeoutSeconds int) error {
	return ErrNop
}

func (o NopOperator) SetReadOnly(context.Context, bool) error {
	return ErrNop
}

func (o NopOperator) KillConnections(context.Context) error {
	return ErrNop
}
