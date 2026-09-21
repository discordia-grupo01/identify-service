package handler

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

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
			log.Printf("profile update failed for user %v: %v", userID, err)
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
