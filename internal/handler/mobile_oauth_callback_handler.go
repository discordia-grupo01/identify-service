package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// mobileOAuthCallbackHTML es la pagina "de rebote": Google Cloud Console solo
// acepta redirect_uri https:// para un Client ID tipo "Web application", pero
// Expo Go solo puede volver a exp://. Esta pagina es la
// que Google sí acepta; una vez ahi, JS del lado del navegador del celular
// arma el salto final a exp://, algo que el navegador si permite

const mobileOAuthCallbackHTML = `<!doctype html>
<html>
  <body>
    <script>
      var params = new URLSearchParams(window.location.hash.slice(1));
      var appRedirect = new URLSearchParams(window.location.search).get("app_redirect");
      if (appRedirect) {
        window.location.href = appRedirect + "?" + params.toString();
      }
    </script>
  </body>
</html>`

// MobileOAuthCallback sirve la pagina de rebote en GET /oauth/mobile-callback.
func MobileOAuthCallback(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(mobileOAuthCallbackHTML))
}
