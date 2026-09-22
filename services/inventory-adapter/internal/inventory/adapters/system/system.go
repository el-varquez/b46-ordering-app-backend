package system

import (
	"time"

	"github.com/google/uuid"
)

type IDs struct{}

func (IDs) New() uuid.UUID { return uuid.New() }

type Clock struct{}

func (Clock) Now() time.Time { return time.Now().UTC() }
