package captcha

import (
	"net/http"
	"time"

	"github.com/google/wire"

	"github.com/authgear/authgear-server/pkg/util/httputil"
)

type HTTPClient struct {
	*http.Client
}

func NewHTTPClient() HTTPClient {
	return HTTPClient{
		httputil.NewExternalClient(5 * time.Second),
	}
}

var DependencySet = wire.NewSet(
	NewHTTPClient,
	NewCloudflareClient,
)
