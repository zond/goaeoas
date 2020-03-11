package goaeoas

import (
	"testing"

	"github.com/gorilla/mux"
)

var (
	userResource *Resource
)

const (
	ListAllUsersRoute = "ListAllUsers"
	ListFriendsRoute  = "ListFriends"
)

type Image struct {
	Mime string
	URL  string
}

type Address struct {
	City    string
	Zip     int
	Country string
	Tags    []string
	Images  map[string]Image
}

type User struct {
	Name      string `methods:"POST"`
	Phone     string `methods:"POST,PUT"`
	IsAdmin   bool
	Addresses []Address
}

func (u *User) Item(r Request) *Item {
	return NewItem(u)
}

func createUser(w ResponseWriter, r Request) (*User, error) {
	return nil, nil
}

func updateUser(w ResponseWriter, r Request) (*User, error) {
	return nil, nil
}

func loadUser(w ResponseWriter, r Request) (*User, error) {
	return nil, nil
}

func deleteUser(w ResponseWriter, r Request) (*User, error) {
	return nil, nil
}

func listAllUsers(w ResponseWriter, r Request) error {
	return nil
}

func listFriends(w ResponseWriter, r Request) error {
	return nil
}

func init() {
	userResource = &Resource{
		Create:     createUser,
		Load:       loadUser,
		Update:     updateUser,
		Delete:     deleteUser,
		FullPath:   "/User/{user_id}",
		CreatePath: "/User",
		Listers: []Lister{
			{
				Path:    "/Users/All",
				Route:   ListAllUsersRoute,
				Handler: listAllUsers,
			},
			{
				Path:    "/Users/Friends",
				Route:   ListFriendsRoute,
				Handler: listFriends,
			},
		},
	}
	router = mux.NewRouter()
	HandleResource(router, userResource)
}

func TestToJava(t *testing.T) {
	_, err := userResource.toJavaInterface("user")
	if err != nil {
		t.Fatal(err)
	}
	_, err = userResource.toJavaClasses("user", "")
	if err != nil {
		t.Fatal(err)
	}
}
