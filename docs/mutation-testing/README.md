# Mutation testing de identify

## Qué es y para qué sirve

La cobertura dice qué líneas **se ejecutan** durante los tests, pero no si los
tests **revisan** lo que pasa en ellas. El mutation testing mide eso otro: la
herramienta mete a propósito errores chicos en el código, uno por vez (cambia
`>` por `>=`, niega un `if`, cambia `||` por `&&`, `*=` por `/=`...), y corre
los tests con cada uno. Cada versión con un error se llama **mutante**:

- Si algún test falla, el mutante quedó **muerto** (`KILLED`): los tests
  detectaron el error.
- Si todos pasan, **sobrevivió** (`LIVED`): ese error podría llegar a
  producción sin que nada avise.

La **efectividad** es muertos / (muertos + sobrevivientes).

## Herramienta y alcance

Usamos [gremlins](https://github.com/go-gremlins/gremlins), una herramienta de
mutation testing para Go, sobre `internal/service`: registro, login, 2FA,
confirmación de mail, recuperación de contraseña y perfil. Activamos también
los mutantes opcionales (operadores lógicos, asignaciones, `continue`/`break`,
operaciones de bits).

## Cómo correrlo

```bash
go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
make mutation
```

Tarda unos 10 minutos (los tests usan bcrypt, que es lento a propósito) y no
está en el CI. Los tests de `internal/service` son unitarios: no usan base de
datos ni RabbitMQ.

**Lo que nos pasó la primera vez:** la primera corrida dio 0 sobrevivientes y
66 `TIMED OUT`. Parecía perfecto, pero no lo era. gremlins calcula cuánto
esperar a cada mutante a partir de lo que tardan los tests, y Go le devolvió el
resultado guardado en caché: midió 0,34 s cuando en realidad tardan 14 s. Con
esa espera tan corta, muchos mutantes "se pasaban de tiempo" sin que les
pasara nada, y los que habrían sobrevivido quedaban escondidos ahí. Limpiando
la caché antes (`go clean -testcache`, ya está en `make mutation`) la corrida
real dio **57 sobrevivientes**. Moraleja: muchos `TIMED OUT` no son una buena
noticia, hay que mirarlos.

## Resultados

Archivos `before.*` (corrida real, con la caché limpia) y `after.*` (después de
agregar tests).

| | Antes | Después |
|---|---|---|
| Muertos | 294 | 346 |
| Sobrevivientes | 57 | 6 |
| No cubiertos | 13 | 12 |
| Timeout | 0 | 0 |
| **Efectividad** | **83,76%** | **98,30%** |

Los 6 que siguen vivos son equivalentes (ver abajo): sin contarlos, los tests
detectan **todos** los mutantes que cambian el comportamiento.

### Los 57 sobrevivientes

**51 eran tests que faltaban** (ya están agregados):

| Grupo | El error que nadie detectaba | Tests nuevos |
|---|---|---|
| Demora después de varios logins fallidos (`auth_service.go` 173-250, 13 mutantes) | Cambiar cuánto se espera (no duplicar, no esperar, esperar desde el primer fallo) o la ventana de tiempo en la que se cuentan los fallos. Es la protección contra fuerza bruta del ADR 10 y ningún test medía la demora. | `login_backoff_test.go`: tabla de `loginBackoff`, que el login espere antes de revisar la contraseña, y que los fallos se cuenten en la ventana de 15 minutos y en la de 1 hora |
| Límites de seguridad (5 mutantes) | Permitir un mail de confirmación más de los 3 por hora (el test usaba 10, lejos del límite), o contar en otra ventana de tiempo; darle un intento más a un desafío 2FA que ya llegó al tope; rechazar una contraseña de exactamente 8 caracteres | `TestEmailConfirmationServiceRequestLimitBoundary`, `TestVerifyLoginRejectsAChallengeAtTheAttemptsCap`, `TestValidatePasswordMinimumLength` |
| Lectura de la configuración (`password_recovery_service.go`, `email_confirmation_service.go`, 23 mutantes) | Aceptar valores inválidos en las variables de entorno (vencimientos en 0 o negativos, límites en 0, URLs sin `https://` o sin dominio). No había tests de estas funciones. | `config_env_test.go` |
| Perfil (`profile_service.go`, 9 mutantes) | Aceptar una descripción, un estado o un emoji un carácter más largo que el máximo, o una imagen de un byte o un píxel de más; y, si falla guardar el perfil, **borrar el avatar que el usuario ya tenía** | `TestProfileTextLimits`, `TestValidateProfileImageLimits`, `TestProfileServiceKeepsTheCurrentAvatarWhenSavingFails` |
| Desactivar 2FA (`two_factor_service.go:394`) | Con un usuario que no existe, seguir de largo y romper (panic) en vez de responder "contraseña incorrecta" | `TestDisableRejectsAPasswordForAnUnknownUser` |

Para medir las ventanas de tiempo, los stubs de los repositorios ahora guardan
desde cuándo se les pidió contar.

En los límites de texto usamos `á`: así el test también verifica que se cuentan
**caracteres** y no bytes (en UTF-8, cada `á` ocupa 2 bytes). El emoji es la
excepción: su límite es en bytes, a propósito.

**6 son mutantes equivalentes**: el cambio no altera nada que se pueda
comprobar.

| Dónde | Por qué es equivalente |
|---|---|
| `auth_service.go:186` | Cambia `delay > 0` por `delay >= 0`: esperar 0 segundos es lo mismo que no esperar |
| `auth_service.go:250` | Cambia `>=` por `>` en el tope de la demora: en los dos casos termina devolviendo el tope |
| `password_recovery_service.go:162` | Cambia `>` por `>=` en el vencimiento máximo: si el valor es justo el máximo, devolver el valor o el máximo es lo mismo |
| `email_confirmation_service.go:156` | El error de `rand.Read`: no hay forma de hacer fallar al generador de números aleatorios del sistema desde un test |
| `profile_service.go:267` (2) | Los decodificadores de imágenes nunca devuelven ancho o alto 0 sin devolver también un error (y con error, las dimensiones valen 0), así que cambiar `\|\|` por `&&` termina rechazando igual |

### Algo que encontramos de paso

`formatExpiration` arma el texto del vencimiento en los mails. Con 1 hora dice
"1 hora", pero con 2 horas diría "2h0m". Hoy se usa 1 hora; si se configura
otro valor, conviene revisarlo.

### Lo que no cuenta

Los no cubiertos son constantes (como `15 * time.Minute`), que la cobertura de
Go no mide.
