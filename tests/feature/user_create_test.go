package feature

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/services"
	"goravel/tests"
)

type UserCreateTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestUserCreateTestSuite(t *testing.T) {
	suite.Run(t, new(UserCreateTestSuite))
}

func (s *UserCreateTestSuite) SetupTest() {
	s.RefreshDatabase()
}

// withStdin runs fn with text on stdin, as `printf ... | echowa artisan`.
func (s *UserCreateTestSuite) withStdin(text string, fn func() error) error {
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	_, err = w.WriteString(text)
	s.Require().NoError(err)
	s.Require().NoError(w.Close())
	original := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = original }()

	return fn()
}

func (s *UserCreateTestSuite) TestUnattendedCreateThenReset() {
	err := s.withStdin("first-password\n", func() error {
		return facades.Artisan().Call("--no-ansi user:create --email Admin@Example.test --password-stdin")
	})
	s.Require().NoError(err)
	user, err := services.FindUserByEmail("admin@example.test")
	s.Require().NoError(err)
	s.Require().NotNil(user)
	s.Equal("Admin", user.Name)
	s.True(facades.Hash().Check("first-password", user.Password))

	err = s.withStdin("second-password\r\n", func() error {
		return facades.Artisan().Call("--no-ansi user:create --email admin@example.test --name Louis --password-stdin")
	})
	s.Require().NoError(err)
	user, err = services.FindUserByEmail("admin@example.test")
	s.Require().NoError(err)
	s.Equal("Louis", user.Name)
	s.True(facades.Hash().Check("second-password", user.Password), "the line ending isn't part of the password")
}

func (s *UserCreateTestSuite) TestUnattendedNeedsStdinAndAValidPassword() {
	s.Error(facades.Artisan().Call("--no-ansi user:create --email admin@example.test"))

	err := s.withStdin("short\n", func() error {
		return facades.Artisan().Call("--no-ansi user:create --email admin@example.test --password-stdin")
	})
	s.ErrorContains(err, "at least 8 characters")

	user, err := services.FindUserByEmail("admin@example.test")
	s.Require().NoError(err)
	s.Nil(user, "nothing is saved on failure")
}
