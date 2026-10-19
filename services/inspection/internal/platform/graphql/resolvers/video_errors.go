package resolvers

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"

	"inspection/services/inspection/internal/platform/apperror"
)

func videoDatabaseFailure(err error) error {
	if err == nil {
		return nil
	}
	var known *apperror.Error
	if errors.As(err, &known) {
		return err
	}
	var networkError net.Error
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, driver.ErrBadConn) || errors.As(err, &networkError) {
		return apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	return err
}
