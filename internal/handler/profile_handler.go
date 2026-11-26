package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/discordia-grupo01/identify-service/internal/apierr"
	"github.com/discordia-grupo01/identify-service/internal/logging"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// const maxProfileImageBytes = 5 * 1024 * 1024 // 5 MiB

type ProfileHandler struct {
	service service.ProfileService
}

func NewProfileHandler(profileService service.ProfileService) *ProfileHandler {
	return &ProfileHandler{service: profileService}
}

// GetProfile godoc
// @Summary      Get my own profile
// @Tags         profile
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  model.UserResponse
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      404  {object}  apierr.ErrorResponse
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/profile [get]
func (h *ProfileHandler) GetProfile(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		apierr.Unauthorized(c)
		return
	}

	user, err := h.service.GetProfile(c.Request.Context(), userID.(string))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			apierr.Abort(c, http.StatusNotFound, apierr.CodeUserNotFound, "El usuario no existe", nil)
		default:
			apierr.Internal(c)
		}
		return
	}
	c.JSON(http.StatusOK, user)
}

// GetPublicProfile godoc
// @Summary      Get another user's public profile
// @Description  Only the fields listed as public (name, photo, description, status, mutual servers) -- never email or registration date.
// @Tags         profile
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Target user's ID"
// @Success      200  {object}  model.PublicUserResponse
// @Failure      400  {object}  apierr.ErrorResponse  "invalid user id"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      403  {object}  apierr.ErrorResponse  "the caller's own account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      404  {object}  apierr.ErrorResponse  "no such user, or the user is suspended"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/users/{id} [get]
func (h *ProfileHandler) GetPublicProfile(c *gin.Context) {
	viewerUserID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		apierr.Unauthorized(c)
		return
	}

	targetUserID := c.Param("id")
	if _, err := uuid.Parse(targetUserID); err != nil {
		apierr.InvalidParameter(c, "id", "El id de usuario no es válido")
		return
	}

	user, err := h.service.GetPublicProfile(c.Request.Context(), viewerUserID.(string), targetUserID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			apierr.Abort(c, http.StatusNotFound, apierr.CodeUserNotFound, "El usuario no existe", nil)
		default:
			apierr.Internal(c)
		}
		return
	}
	c.JSON(http.StatusOK, user)
}

// Update godoc
// @Summary      Edit my own profile
// @Description  Multipart form, all fields optional -- omitted fields are left unchanged.
// @Tags         profile
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        name         formData  string  false  "New display name"
// @Param        description  formData  string  false  "New description"
// @Param        image        formData  file    false  "New profile image (JPEG, PNG or WebP, up to 5 MiB)"
// @Param        remove_image formData  boolean false  "Remove the current profile image"
// @Success      200  {object}  model.UserResponse
// @Failure      400  {object}  apierr.ErrorResponse  "invalid name/description/image, or nothing to change"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      404  {object}  apierr.ErrorResponse
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/profile [patch]
func (h *ProfileHandler) Update(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		apierr.Unauthorized(c)
		return
	}

	request := model.ProfileUpdateRequest{}
	if name, exists := c.GetPostForm("name"); exists && strings.TrimSpace(name) != "" {
		request.Name = &name
	}
	if description, exists := c.GetPostForm("description"); exists {
		request.Description = &description
	}
	if removeImage, exists := c.GetPostForm("remove_image"); exists {
		parsed, err := strconv.ParseBool(removeImage)
		if err != nil {
			writeProfileInputError(c, errors.New("remove_image debe ser booleano"))
			return
		}
		request.RemoveImage = parsed
	}

	fileHeader, err := c.FormFile("image")
	if err == nil {
		if fileHeader.Size > service.MaxProfileImageBytes {
			writeProfileInputError(c, service.ErrInvalidProfileImage)
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			writeProfileInputError(c, service.ErrInvalidProfileImage)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, service.MaxProfileImageBytes+1))
		if err != nil || len(data) > service.MaxProfileImageBytes {
			writeProfileInputError(c, service.ErrInvalidProfileImage)
			return
		}
		request.Image = data
		request.ImageContentType = http.DetectContentType(data)
		request.ImageProvided = true
	} else if !errors.Is(err, http.ErrMissingFile) && err.Error() != "http: no such file" {
		writeProfileInputError(c, service.ErrInvalidProfileImage)
		return
	}

	response, err := h.service.Update(c.Request.Context(), userID.(string), request)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidProfileName), errors.Is(err, service.ErrInvalidProfileDescription), errors.Is(err, service.ErrInvalidProfileImage), errors.Is(err, service.ErrNoProfileChanges):
			writeProfileInputError(c, err)
		case errors.Is(err, service.ErrUserNotFound):
			apierr.Abort(c, http.StatusNotFound, apierr.CodeUserNotFound, "El usuario no existe", nil)
		default:
			logging.Errorf("profile update failed for user %v: %v", userID, err)
			apierr.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

func writeProfileInputError(c *gin.Context, err error) {
	apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidProfile, err.Error(), nil)
}

// UpdateStatus godoc
// @Summary      Set my custom status
// @Description  A nil field means "don't touch it"; an empty string means "clear it".
// @Tags         profile
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  model.StatusUpdateRequest  true  "Status text and/or emoji"
// @Success      200  {object}  model.UserResponse
// @Failure      400  {object}  apierr.ErrorResponse  "invalid text/emoji, or no changes"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      404  {object}  apierr.ErrorResponse
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/status [put]
func (h *ProfileHandler) UpdateStatus(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		apierr.Unauthorized(c)
		return
	}

	var request model.StatusUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apierr.InvalidBodyFromBinding(c, "El cuerpo de la petición no es un JSON válido", err)
		return
	}

	response, err := h.service.UpdateStatus(c.Request.Context(), userID.(string), request)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNoStatusChanges), errors.Is(err, service.ErrInvalidStatusText), errors.Is(err, service.ErrInvalidStatusEmoji):
			writeStatusInputError(c, err)
		case errors.Is(err, service.ErrUserNotFound):
			apierr.Abort(c, http.StatusNotFound, apierr.CodeUserNotFound, "El usuario no existe", nil)
		default:
			apierr.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

// DeleteStatus godoc
// @Summary      Clear my custom status
// @Tags         profile
// @Security     BearerAuth
// @Success      204  "no content"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      404  {object}  apierr.ErrorResponse
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/status [delete]
func (h *ProfileHandler) DeleteStatus(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		apierr.Unauthorized(c)
		return
	}

	if err := h.service.ClearStatus(c.Request.Context(), userID.(string)); err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			apierr.Abort(c, http.StatusNotFound, apierr.CodeUserNotFound, "El usuario no existe", nil)
		default:
			apierr.Internal(c)
		}
		return
	}

	c.Status(http.StatusNoContent)
}

func writeStatusInputError(c *gin.Context, err error) {
	apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidStatus, err.Error(), nil)
}
