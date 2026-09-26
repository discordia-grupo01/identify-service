package email

import (
	"bytes"
	"fmt"
	"html/template"
	"net/mail"
	"strings"
	texttemplate "text/template"
)

type PasswordResetRequest struct {
	Email     string
	Name      string
	ResetURL  string
	ExpiresIn string
}

type EmailConfirmationRequest struct {
	Email      string
	Name       string
	ConfirmURL string
	ExpiresIn  string
}

type passwordResetData struct {
	AppName   string
	Name      string
	ResetURL  string
	ExpiresIn string
}

type emailConfirmationData struct {
	AppName    string
	Name       string
	ConfirmURL string
	ExpiresIn  string
}

func renderPasswordResetEmail(config Config, request PasswordResetRequest) ([]byte, error) {
	if _, err := mail.ParseAddress(request.Email); err != nil {
		return nil, fmt.Errorf("invalid recipient email: %w", err)
	}
	if request.ResetURL == "" {
		return nil, fmt.Errorf("reset URL is required")
	}

	data := passwordResetData{
		AppName:   config.AppName,
		Name:      request.Name,
		ResetURL:  request.ResetURL,
		ExpiresIn: request.ExpiresIn,
	}
	if data.AppName == "" {
		data.AppName = "Discordia"
	}
	if data.ExpiresIn == "" {
		data.ExpiresIn = "1 hora"
	}

	htmlBody, err := renderHTML(data)
	if err != nil {
		return nil, err
	}
	textBody, err := renderText(data)
	if err != nil {
		return nil, err
	}

	from := config.From
	if config.AppName != "" {
		from = fmt.Sprintf("%s <%s>", config.AppName, config.From)
	}
	var message strings.Builder
	fmt.Fprintf(&message, "From: %s\r\n", from)
	fmt.Fprintf(&message, "To: %s\r\n", request.Email)
	if config.ReplyTo != "" {
		fmt.Fprintf(&message, "Reply-To: %s\r\n", config.ReplyTo)
	}
	message.WriteString("Subject: Recupera tu contrasena de ")
	message.WriteString(data.AppName)
	message.WriteString("\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=discordia-boundary\r\n\r\n")
	message.WriteString("--discordia-boundary\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	message.WriteString(textBody)
	message.WriteString("\r\n--discordia-boundary\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n")
	message.WriteString(htmlBody)
	message.WriteString("\r\n--discordia-boundary--\r\n")
	return []byte(message.String()), nil
}

func renderHTML(data passwordResetData) (string, error) {
	template, err := template.New("password-reset").Parse(passwordResetHTML)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := template.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

func renderText(data passwordResetData) (string, error) {
	template, err := texttemplate.New("password-reset").Parse(passwordResetText)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := template.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

func renderEmailConfirmationEmail(config Config, request EmailConfirmationRequest) ([]byte, error) {
	if _, err := mail.ParseAddress(request.Email); err != nil {
		return nil, fmt.Errorf("invalid recipient email: %w", err)
	}
	if request.ConfirmURL == "" {
		return nil, fmt.Errorf("confirmation URL is required")
	}
	if request.ExpiresIn == "" {
		return nil, fmt.Errorf("expiresIn is required")
	}

	data := emailConfirmationData{
		AppName:    config.AppName,
		Name:       request.Name,
		ConfirmURL: request.ConfirmURL,
		ExpiresIn:  request.ExpiresIn,
	}
	if data.AppName == "" {
		data.AppName = "Discordia"
	}
	if data.ExpiresIn == "" {
		data.ExpiresIn = "24 horas"
	}

	htmlBody, err := renderConfirmationHTML(data)
	if err != nil {
		return nil, err
	}
	textBody, err := renderConfirmationText(data)
	if err != nil {
		return nil, err
	}

	from := config.From
	if config.AppName != "" {
		from = fmt.Sprintf("%s <%s>", config.AppName, config.From)
	}
	var message strings.Builder
	fmt.Fprintf(&message, "From: %s\r\n", from)
	fmt.Fprintf(&message, "To: %s\r\n", request.Email)
	if config.ReplyTo != "" {
		fmt.Fprintf(&message, "Reply-To: %s\r\n", config.ReplyTo)
	}
	message.WriteString("Subject: Confirma tu correo de ")
	message.WriteString(data.AppName)
	message.WriteString("\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=discordia-boundary\r\n\r\n")
	message.WriteString("--discordia-boundary\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	message.WriteString(textBody)
	message.WriteString("\r\n--discordia-boundary\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n")
	message.WriteString(htmlBody)
	message.WriteString("\r\n--discordia-boundary--\r\n")
	return []byte(message.String()), nil
}

func renderConfirmationHTML(data emailConfirmationData) (string, error) {
	template, err := template.New("email-confirmation").Parse(emailConfirmationHTML)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := template.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

func renderConfirmationText(data emailConfirmationData) (string, error) {
	template, err := texttemplate.New("email-confirmation").Parse(emailConfirmationText)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := template.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

const passwordResetText = `Hola {{if .Name}}{{.Name}}{{else}}ahi{{end}},

Recibimos una solicitud para cambiar la contrasena de tu cuenta de {{.AppName}}.

Abre este enlace para elegir una nueva contrasena:
{{.ResetURL}}

El enlace vence en {{.ExpiresIn}}. Si no solicitaste este cambio, puedes ignorar este correo.

El equipo de {{.AppName}}`

const passwordResetHTML = `<!doctype html>
<html lang="es">
<body style="margin:0;background:#0f1922;color:#f5f2eb;font-family:Inter,Arial,sans-serif;">
  <div style="padding:40px 16px;background:#0f1922;">
    <div style="max-width:560px;margin:0 auto;background:#1e2d42;border:1px solid rgba(168,198,223,.18);border-radius:16px;overflow:hidden;">
      <div style="padding:28px 32px;background:linear-gradient(135deg,#245c6b,#2d7a8c);">
        <div style="font-family:Outfit,Arial,sans-serif;font-size:24px;font-weight:700;letter-spacing:.2px;">{{.AppName}}</div>
        <div style="margin-top:8px;color:#d8edf2;font-size:14px;">Tu espacio para conectar.</div>
      </div>
      <div style="padding:36px 32px 32px;">
        <div style="color:#a8c6df;font-size:12px;font-weight:700;letter-spacing:1.6px;text-transform:uppercase;">Seguridad de tu cuenta</div>
        <h1 style="margin:12px 0 16px;font-family:Outfit,Arial,sans-serif;font-size:30px;line-height:1.15;color:#f5f2eb;">Restablece tu contrasena</h1>
        <p style="margin:0 0 16px;font-size:16px;line-height:1.6;color:#d7e4ed;">Hola {{if .Name}}{{.Name}}{{else}}ahi{{end}}, recibimos una solicitud para cambiar la contrasena de tu cuenta.</p>
        <p style="margin:0 0 28px;font-size:16px;line-height:1.6;color:#d7e4ed;">Usa el boton para elegir una nueva. El enlace vence en <strong style="color:#fce3a4;">{{.ExpiresIn}}</strong>.</p>
        <a href="{{.ResetURL}}" style="display:inline-block;padding:14px 22px;border-radius:12px;background:#2d7a8c;color:#fff;text-decoration:none;font-family:Outfit,Arial,sans-serif;font-weight:700;">Elegir nueva contrasena</a>
        <p style="margin:28px 0 0;padding-top:20px;border-top:1px solid rgba(168,198,223,.12);font-size:13px;line-height:1.6;color:#a8c6df;">Si no solicitaste este cambio, ignora este correo. Tu contrasena actual seguira siendo valida.</p>
      </div>
    </div>
    <p style="max-width:560px;margin:18px auto 0;text-align:center;font-size:12px;color:rgba(168,198,223,.65);">Correo automatico de {{.AppName}}</p>
  </div>
</body>
</html>`

const emailConfirmationText = `Hola {{if .Name}}{{.Name}}{{else}}ahi{{end}},

Gracias por unirte a {{.AppName}}. Para confirmar tu cuenta y empezar a usarla, abre este enlace:
{{.ConfirmURL}}

Este enlace vence en {{.ExpiresIn}}. Si no creaste esta cuenta, puedes ignorar este correo.

El equipo de {{.AppName}}`

const emailConfirmationHTML = `<!doctype html>
<html lang="es">
<body style="margin:0;background:#0f1922;color:#f5f2eb;font-family:Inter,Arial,sans-serif;">
	<div style="padding:40px 16px;background:#0f1922;">
		<div style="max-width:560px;margin:0 auto;background:#1e2d42;border:1px solid rgba(168,198,223,.18);border-radius:16px;overflow:hidden;">
			<div style="padding:28px 32px;background:linear-gradient(135deg,#245c6b,#2d7a8c);">
				<div style="font-family:Outfit,Arial,sans-serif;font-size:24px;font-weight:700;letter-spacing:.2px;">{{.AppName}}</div>
				<div style="margin-top:8px;color:#d8edf2;font-size:14px;">Tu espacio para conectar.</div>
			</div>
			<div style="padding:36px 32px 32px;">
				<div style="color:#a8c6df;font-size:12px;font-weight:700;letter-spacing:1.6px;text-transform:uppercase;">Confirma tu cuenta</div>
				<h1 style="margin:12px 0 16px;font-family:Outfit,Arial,sans-serif;font-size:30px;line-height:1.15;color:#f5f2eb;">Verifica tu correo</h1>
				<p style="margin:0 0 16px;font-size:16px;line-height:1.6;color:#d7e4ed;">Hola {{if .Name}}{{.Name}}{{else}}ahi{{end}}, gracias por unirte a {{.AppName}}.</p>
				<p style="margin:0 0 28px;font-size:16px;line-height:1.6;color:#d7e4ed;">Activa tu cuenta con el siguiente enlace. El enlace vence en <strong style="color:#fce3a4;">{{.ExpiresIn}}</strong>.</p>
				<a href="{{.ConfirmURL}}" style="display:inline-block;padding:14px 22px;border-radius:12px;background:#2d7a8c;color:#fff;text-decoration:none;font-family:Outfit,Arial,sans-serif;font-weight:700;">Confirmar mi correo</a>
				<p style="margin:28px 0 0;padding-top:20px;border-top:1px solid rgba(168,198,223,.12);font-size:13px;line-height:1.6;color:#a8c6df;">Si no creaste esta cuenta, puedes ignorar este correo.</p>
			</div>
		</div>
		<p style="max-width:560px;margin:18px auto 0;text-align:center;font-size:12px;color:rgba(168,198,223,.65);">Correo automatico de {{.AppName}}</p>
	</div>
</body>
</html>`
