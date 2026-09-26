package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// mobileOAuthCallbackHTML es la pagina "de rebote": Google Cloud Console solo
// acepta redirect_uri https:// para un Client ID tipo "Web application", pero
// Expo Go solo puede volver a exp://. Esta pagina es la
// que Google sí acepta; una vez ahi, JS del lado del navegador del celular
// arma el salto final a exp://, algo que el navegador si permite.
//
// El exp://... real viaja en el parametro `state` de OAuth (no como query
// param del redirect_uri, porque Google exige match exacto contra la URL
// registrada). Con response_type=id_token Google devuelve todo en el
// fragmento (#state=...&id_token=...), que nunca llega al servidor: por eso
// se lee desde JS. Se reenvian todos los parametros, `state` incluido,
// porque expo-auth-session valida que coincida con el que mando. Solo se
// redirige a esquemas de la app para que la pagina no sirva de open redirect.
//
// En Android el login corre dentro de una Chrome Custom Tab, y ahi un salto
// automatico (sin toque del usuario) a exp://... le llega a la app recortado
// a exp://<host>:<puerto>, sin path ni parametros. Con un link intent://
// Chrome se lo pasa completo, asi que en Android usamos ese formato (los
// parametros van en el query porque el # lo usa la sintaxis de intent).
const mobileOAuthCallbackHTML = `<!doctype html>
<html>
  <body>
    <p id="msg">Volviendo a la app...</p>
    <a id="link" style="display:none">Abrir la app</a>
    <script>
      var params = new URLSearchParams(window.location.hash.slice(1));
      var appRedirect = params.get("state");
      var match = appRedirect && appRedirect.match(/^(exp|exps|discordia):\/\/(.*)$/);
      if (match) {
        var target = appRedirect + "#" + params.toString();
        if (/Android/i.test(navigator.userAgent)) {
          var scheme = match[1];
          var pkg = scheme === "discordia" ? "" : "package=host.exp.exponent;";
          target = "intent://" + match[2] + "?" + params.toString() +
            "#Intent;scheme=" + scheme + ";" + pkg + "end";
        }
        window.location.replace(target);
        // Fallback: algunos navegadores bloquean el salto automatico a esquemas custom.
        var link = document.getElementById("link");
        link.href = target;
        link.style.display = "inline";
      } else {
        document.getElementById("msg").textContent = "Respuesta de login invalida.";
      }
    </script>
  </body>
</html>`

// MobileOAuthCallback godoc
// @Summary      OAuth bounce page for the mobile app (Expo Go)
// @Description  Serves an HTML page that reads the `state`/`id_token` OAuth fragment and redirects to the app's exp:// (or discordia://) scheme -- Google only accepts https:// redirect URIs, Expo Go only understands exp://.
// @Tags         oauth
// @Produce      html
// @Success      200  {string}  string  "HTML page"
// @Router       /oauth/mobile-callback [get]
func MobileOAuthCallback(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(mobileOAuthCallbackHTML))
}
