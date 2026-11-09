package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

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
// @Failure      401  {object}  model.ErrorResponse
// @Failure      404  {object}  model.ErrorResponse
// @Router       /v1/me/profile [get]
func (h *ProfileHandler) GetProfile(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
			Code:    "UNAUTHORIZED",
			Message: "La sesión no es válida",
		}})
		return
	}

	user, err := h.service.GetProfile(c.Request.Context(), userID.(string))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			c.JSON(http.StatusNotFound, model.ErrorResponse{Error: model.APIError{
				Code:    "USER_NOT_FOUND",
				Message: "El usuario no existe",
			}})
		default:
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Ha ocurrido un error inesperado en el servidor",
			}})
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
// @Failure      400  {object}  model.ErrorResponse  "invalid user id"
// @Failure      401  {object}  model.ErrorResponse
// @Failure      403  {object}  model.ErrorResponse  "the caller's own account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      404  {object}  model.ErrorResponse  "no such user, or the user is suspended"
// @Router       /v1/users/{id} [get]
func (h *ProfileHandler) GetPublicProfile(c *gin.Context) {
	viewerUserID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
			Code:    "UNAUTHORIZED",
			Message: "La sesión no es válida",
		}})
		return
	}

	targetUserID := c.Param("id")
	if _, err := uuid.Parse(targetUserID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
			Code:    "INVALID_USER_ID",
			Message: "El id de usuario no es válido",
		}})
		return
	}

	user, err := h.service.GetPublicProfile(c.Request.Context(), viewerUserID.(string), targetUserID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			c.JSON(http.StatusNotFound, model.ErrorResponse{Error: model.APIError{
				Code:    "USER_NOT_FOUND",
				Message: "El usuario no existe",
			}})
		default:
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Ha ocurrido un error inesperado en el servidor",
			}})
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
// @Failure      400  {object}  model.ErrorResponse  "invalid name/description/image"
// @Failure      401  {object}  model.ErrorResponse
// @Failure      404  {object}  model.ErrorResponse
// @Router       /v1/me/profile [patch]
func (h *ProfileHandler) Update(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
			Code:    "UNAUTHORIZED",
			Message: "La sesión no es válida",
		}})
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
		case errors.Is(err, service.ErrInvalidProfileName), errors.Is(err, service.ErrInvalidProfileDescription), errors.Is(err, service.ErrInvalidProfileImage):
			writeProfileInputError(c, err)
		case errors.Is(err, service.ErrUserNotFound):
			c.JSON(http.StatusNotFound, model.ErrorResponse{Error: model.APIError{
				Code:    "USER_NOT_FOUND",
				Message: "El usuario no existe",
			}})
		default:
			logging.Errorf("profile update failed for user %v: %v", userID, err)
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Ha ocurrido un error inesperado en el servidor",
			}})
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

func writeProfileInputError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
		Code:    "INVALID_PROFILE",
		Message: err.Error(),
	}})
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
// @Failure      400  {object}  model.ErrorResponse  "invalid text/emoji, or no changes"
// @Failure      401  {object}  model.ErrorResponse
// @Failure      404  {object}  model.ErrorResponse
// @Router       /v1/me/status [put]
func (h *ProfileHandler) UpdateStatus(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
			Code:    "UNAUTHORIZED",
			Message: "La sesión no es válida",
		}})
		return
	}

	var request model.StatusUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeStatusInputError(c, errors.New("el cuerpo de la petición no es un JSON válido"))
		return
	}

	response, err := h.service.UpdateStatus(c.Request.Context(), userID.(string), request)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNoStatusChanges), errors.Is(err, service.ErrInvalidStatusText), errors.Is(err, service.ErrInvalidStatusEmoji):
			writeStatusInputError(c, err)
		case errors.Is(err, service.ErrUserNotFound):
			c.JSON(http.StatusNotFound, model.ErrorResponse{Error: model.APIError{
				Code:    "USER_NOT_FOUND",
				Message: "El usuario no existe",
			}})
		default:
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Ha ocurrido un error inesperado en el servidor",
			}})
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
// @Failure      401  {object}  model.ErrorResponse
// @Failure      404  {object}  model.ErrorResponse
// @Router       /v1/me/status [delete]
func (h *ProfileHandler) DeleteStatus(c *gin.Context) {
	userID, exists := c.Get(middleware.UserIDKey)
	if !exists {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
			Code:    "UNAUTHORIZED",
			Message: "La sesión no es válida",
		}})
		return
	}

	if err := h.service.ClearStatus(c.Request.Context(), userID.(string)); err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			c.JSON(http.StatusNotFound, model.ErrorResponse{Error: model.APIError{
				Code:    "USER_NOT_FOUND",
				Message: "El usuario no existe",
			}})
		default:
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Ha ocurrido un error inesperado en el servidor",
			}})
		}
		return
	}

	c.Status(http.StatusNoContent)
}

func writeStatusInputError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
		Code:    "INVALID_STATUS",
		Message: err.Error(),
	}})
}
