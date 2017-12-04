# Gorm Expect

A Go database testing library for use with [`gorm`](https://github.com/jinzhu/gorm) that doesn't involve a lot
of pain.

[![GoDoc](https://godoc.org/github.com/iantanwx/gorm-expect?status.svg)](https://godoc.org/github.com/iantanwx/gorm-expect)

## Why

Testing `gorm`-based DALs is terrible. Most of the time, you have to use
`db.Debug()` to print the SQL generated by gorm, escape it, generate mock
`sql.Rows`, etc., and hope for the best. `gormexpect` wraps and mirrors
(most of) the gorm API for superior DX.

## Installation

```
go get -u github.com/iantanwx/gorm-expect
```

## Usage

```
type User struct {
	Id           int64
	Age          int64
	Name         string `sql:"size:255"`
	Email        string
	Birthday     *time.Time // Time
	CreatedAt    time.Time  // CreatedAt: Time of record is created, will be insert automatically
	UpdatedAt    time.Time  // UpdatedAt: Time of record is updated, will be updated automatically
	Emails       []Email    // Embedded structs
	CreditCard   CreditCard
	Languages    []Language `gorm:"many2many:user_languages;"`
	PasswordHash []byte
}

type UserRepository struct {
  db *gorm.DB
}

func (r *UserRepository) FindByID(id int64) (User, error) {
	user := User{Id: id}
	err := r.db.Preload("Emails").Preload("CreditCard").Preload("Languages").Find(&user).Error
	return user, err
}

func TestUserRepoPreload1(t *testing.T) {
	db, expect, err := expecter.NewDefaultExpecter()
	defer db.Close()

	if err != nil {
		t.Fatal(err)
	}

	repo := &UserRepository{db}

	// has one
	creditCard := CreditCard{Number: "12345678"}
	// has many
	email := []Email{
		Email{Email: "fake_user@live.com"},
		Email{Email: "fake_user@gmail.com"},
	}
	// many to many
	languages := []Language{
		Language{Name: "EN"},
		Language{Name: "ZH"},
	}

	expected := User{
		Id:         1,
		Name:       "my_name",
		CreditCard: creditCard,
		Emails:     email,
		Languages:  languages,
	}

	expect.Preload("Emails").Preload("CreditCard").Preload("Languages").Find(&User{Id: 1}).Returns(expected)
	actual, err := repo.FindByID(1)

	assert.Nil(t, expect.AssertExpectations())
	assert.Nil(t, err)
	assert.Equal(t, expected, actual)
}
