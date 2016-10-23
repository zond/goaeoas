package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	. "github.com/zond/goaeoas"
)

var (
	users     = map[string]*User{}
	usersLock = &sync.RWMutex{}
)

type User struct {
	ID    string
	Name  string
	Email string
}

func (u *User) Item(r Request) *Item {
	return NewItem(u).SetDesc([][]string{[]string{u.Name}}).AddLink(r.NewLink(UserResource.Link("self", Load, u.ID)))
}

func LoadUser(w ResponseWriter, r Request) (*User, error) {
	usersLock.RLock()
	defer usersLock.RUnlock()
	if u, found := users[r.Vars()["id"]]; found {
		return u, nil
	}
	http.Error(w, "not found", 404)
	return nil, nil
}

func CreateUser(w ResponseWriter, r Request) (*User, error) {
	u := &User{}
	if err := json.NewDecoder(r.Req().Body).Decode(u); err != nil {
		return nil, err
	}
	u.ID = fmt.Sprint(len(users))
	usersLock.Lock()
	defer usersLock.Unlock()
	users[u.ID] = u
	return u, nil
}

var UserResource = &Resource{
	Load:   LoadUser,
	Create: CreateUser,
}

func main() {
	r := mux.NewRouter()
	HandleResource(r, UserResource)
	http.ListenAndServe(":8080", r)
}
