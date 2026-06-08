package token

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

const accessTokenTTL = 15 * time.Minute

// jwtClaims define los claims del access token.
// Se usa "tid" y "role" como claims cortos para mantener el token compacto.
type jwtClaims struct {
	jwt.RegisteredClaims
	TenantID string `json:"tid"`
	Role     string `json:"role"`
	Env      string `json:"env"`
}

// JWTIssuer implementa application.TokenIssuer con HMAC-SHA256.
//
// El secret debe tener al menos 32 bytes de entropía.
// En producción debe cargarse desde un secreto seguro (vault, env cifrado),
// nunca hardcodeado.
type JWTIssuer struct {
	secret []byte
}

// NewJWTIssuer crea un JWTIssuer con el secret dado.
func NewJWTIssuer(secret string) (*JWTIssuer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("jwt: secret must be at least 32 characters")
	}
	return &JWTIssuer{secret: []byte(secret)}, nil
}

// IssueAccessToken genera un JWT firmado con HS256 válido por 15 minutos.
func (i *JWTIssuer) IssueAccessToken(c application.AccessTokenClaims) (string, error) {
	now := time.Now().UTC()

	claims := &jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   c.UserID.String(),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			// jti: ID único del token (permite invalidación individual en el futuro).
			ID: uuid.NewString(),
		},
		TenantID: c.TenantID.String(),
		Role:     c.Role.String(),
		Env:      c.Environment.String(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("jwt: sign token: %w", err)
	}
	return signed, nil
}

// IssueRefreshToken genera un secreto aleatorio seguro para el refresh token.
// El caller es responsable de hashearlo antes de persistirlo.
func (i *JWTIssuer) IssueRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("jwt: generate refresh token: %w", err)
	}
	// URL-safe base64 para que sea seguro en cookies/headers sin encoding adicional.
	return base64.URLEncoding.EncodeToString(b), nil
}

// VerifyAccessToken valida la firma y expiración del JWT y extrae los claims.
// Retorna domain.ErrInvalidCredentials para cualquier error de validación,
// sin revelar el detalle al caller (se logea internamente si es necesario).
func (i *JWTIssuer) VerifyAccessToken(tokenStr string) (application.AccessTokenClaims, error) {
	var claims jwtClaims

	token, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
		// Verificar que el algoritmo de firma es el esperado.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.secret, nil
	})

	if err != nil || !token.Valid {
		return application.AccessTokenClaims{}, domain.ErrInvalidCredentials
	}

	// Parsear los claims a tipos del dominio.
	// Usamos conversión directa (sin ParseXxx) porque el token ya fue validado
	// por nosotros mismos al emitirlo; si hay un error aquí es un bug interno.
	userID, err := domain.ParseUserID(claims.Subject)
	if err != nil {
		return application.AccessTokenClaims{}, domain.ErrInvalidCredentials
	}

	tenantID, err := domain.ParseTenantID(claims.TenantID)
	if err != nil {
		return application.AccessTokenClaims{}, domain.ErrInvalidCredentials
	}

	role, err := domain.ParseRole(claims.Role)
	if err != nil {
		return application.AccessTokenClaims{}, domain.ErrInvalidCredentials
	}

	env, err := domain.ParseEnvironment(claims.Env)
	if err != nil {
		return application.AccessTokenClaims{}, domain.ErrInvalidCredentials
	}

	return application.AccessTokenClaims{
		UserID:      userID,
		TenantID:    tenantID,
		Role:        role,
		Environment: env,
	}, nil
}
