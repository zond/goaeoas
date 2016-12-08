package goaeoas

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
)

var (
	pathElementReg = regexp.MustCompile("^([^{]*\\{)([^}]+)(\\}.*$)")
	resources      = []*Resource{}
	nonAlpha       = regexp.MustCompile("[^a-zA-Z0-9]")
)

type Lister struct {
	Path    string
	Route   string
	Handler func(ResponseWriter, Request) error
	// QueryParams document the query params this lister handles, and are only used when generating code or documentation.
	QueryParams []string
}

type Resource struct {
	Create  interface{}
	Update  interface{}
	Delete  interface{}
	Load    interface{}
	Listers []Lister

	FullPath   string
	CreatePath string

	Type reflect.Type
}

func createRoute(ro *mux.Router, re *Resource, meth Method, rType reflect.Type) reflect.Type {
	var fVal reflect.Value
	fVal, rType = validateResourceFunc(re.resourceFunc(meth), rType)
	re.Type = rType
	if re.CreatePath == "" {
		re.CreatePath = fmt.Sprintf("/%s", rType.Name())
	}
	if re.FullPath == "" {
		re.FullPath = fmt.Sprintf("%s/{id}", re.CreatePath)
	}
	pattern := ""
	if meth == Create {
		pattern = re.CreatePath
	} else {
		pattern = re.FullPath
	}
	Handle(
		ro,
		pattern,
		[]string{
			meth.HTTPMethod(),
		},
		re.Route(meth),
		func(w ResponseWriter, r Request) error {
			resultVals := fVal.Call([]reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)})
			if !resultVals[1].IsNil() {
				return resultVals[1].Interface().(error)
			}
			if !resultVals[0].IsNil() {
				w.SetContent(resultVals[0].Interface().(Itemer).Item(r))
			}
			return nil
		},
	)
	return rType
}

func HandleResource(ro *mux.Router, re *Resource) {
	var rType reflect.Type
	if re.Create != nil {
		rType = createRoute(ro, re, Create, rType)
	}
	if re.Update != nil {
		rType = createRoute(ro, re, Update, rType)
	}
	if re.Delete != nil {
		rType = createRoute(ro, re, Delete, rType)
	}
	if re.Load != nil {
		createRoute(ro, re, Load, rType)
	}
	for _, lister := range re.Listers {
		Handle(ro, lister.Path, []string{"GET"}, lister.Route, lister.Handler)
	}
	resources = append(resources, re)
}

func (r *Resource) writeJavaListerMeth(lister Lister, w io.Writer) error {
	ms, err := r.methodSignature(Load, lister.Path, lister.Route, true, lister.QueryParams)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, `  @%s(%q)
  %s;

`, strings.ToUpper(Load.HTTPMethod()), lister.Path, ms)
	return nil
}

func (r *Resource) methodSignature(meth Method, pathTemplate string, route string, plural bool, queryParams []string) (string, error) {
	buf := &bytes.Buffer{}
	args := []string{}
	switch meth {
	case Create:
		args = append(args, fmt.Sprintf("@Body %s %s", r.Type.Name(), strings.ToLower(r.Type.Name())))
	}
	for match := pathElementReg.FindStringSubmatch(pathTemplate); match != nil; match = pathElementReg.FindStringSubmatch(pathTemplate) {
		args = append(args, fmt.Sprintf("@Path(\"%s\") String %s", match[2], match[2]))
		pathTemplate = match[3]
	}
	for _, qp := range queryParams {
		saneQP := nonAlpha.ReplaceAllString(qp, "_")
		args = append(args, fmt.Sprintf("@Query(%q) String %s", qp, saneQP))
	}
	methName := fmt.Sprintf("%s%s%s", r.Type.Name(), strings.ToUpper(string([]rune(meth.String())[0])), strings.ToLower(string([]rune(meth.String())[1:])))
	if route != "" {
		methName = route
	}
	if plural {
		fmt.Fprintf(buf, "Observable<MultiContainer<%s>> %s(%s)", r.Type.Name(), methName, strings.Join(args, ", "))
	} else {
		fmt.Fprintf(buf, "Observable<SingleContainer<%s>> %s(%s)", r.Type.Name(), methName, strings.Join(args, ", "))
	}
	return buf.String(), nil
}

func (r *Resource) writeJavaMeth(meth Method, w io.Writer) error {
	pt, err := router.Get(r.Route(meth)).GetPathTemplate()
	if err != nil {
		return err
	}
	ms, err := r.methodSignature(meth, pt, "", false, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, `  @%s(%q)
  %s;

`, strings.ToUpper(meth.HTTPMethod()), pt, ms)
	return nil
}

func (r *Resource) toJavaClasses(pkg, meth string) (map[string]string, error) {
	docType, err := NewDocType(r.Type, meth)
	if err != nil {
		return nil, err
	}
	return docType.ToJavaClasses(pkg, meth)
}

func (r *Resource) toJavaInterface(pkg string) (string, error) {
	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, `package %s;
	
import retrofit2.http.*;
import rx.*;
	
public interface %sService {
`, pkg, r.Type.Name())
	if r.Create != nil {
		if err := r.writeJavaMeth(Create, buf); err != nil {
			return "", err
		}
	}
	if r.Load != nil {
		if err := r.writeJavaMeth(Load, buf); err != nil {
			return "", err
		}
	}
	if r.Update != nil {
		if err := r.writeJavaMeth(Update, buf); err != nil {
			return "", err
		}
	}
	if r.Delete != nil {
		if err := r.writeJavaMeth(Delete, buf); err != nil {
			return "", err
		}
	}
	for _, lister := range r.Listers {
		if err := r.writeJavaListerMeth(lister, buf); err != nil {
			return "", err
		}
	}
	fmt.Fprint(buf, "}")
	return buf.String(), nil
}

func (r *Resource) resourceFunc(meth Method) interface{} {
	switch meth {
	case Create:
		return r.Create
	case Update:
		return r.Update
	case Delete:
		return r.Delete
	case Load:
		return r.Load
	}
	panic(fmt.Errorf("unknown method %s", meth))
}

func (r *Resource) Route(meth Method) string {
	return r.Type.Name() + "." + meth.String()
}

func (r *Resource) Link(rel string, meth Method, routeParams []string) Link {
	return Link{
		Rel:         rel,
		Route:       r.Route(meth),
		RouteParams: routeParams,
		Method:      meth.HTTPMethod(),
		Type:        r.Type,
	}
}

func (r *Resource) URL(meth Method, id interface{}) (*url.URL, error) {
	return router.Get(r.Route(meth)).URL("id", fmt.Sprint(id))
}

func validateResourceFunc(f interface{}, needType reflect.Type) (fVal reflect.Value, returnType reflect.Type) {
	fVal = reflect.ValueOf(f)
	fTyp := fVal.Type()
	if fTyp.Kind() != reflect.Func {
		panic(fmt.Errorf("%#v isn't a func", f))
	}
	if fTyp.NumIn() != 2 {
		panic(fmt.Errorf("%#v isn't a func with two params", f))
	}
	if !fTyp.In(0).Implements(responseWriterType) {
		panic(fmt.Errorf("%#v isn't a func with a ResponseWriter as its first param", f))
	}
	if !fTyp.In(1).Implements(requestType) {
		panic(fmt.Errorf("%#v isn't a func with a Request as its second param", f))
	}
	if fTyp.NumOut() != 2 {
		panic(fmt.Errorf("%#v isn't a func with two return values", f))
	}
	if !fTyp.Out(0).Implements(itemerType) {
		panic(fmt.Errorf("%#v isn't a func with Itemer as its first return value", f))
	}
	if !fTyp.Out(1).Implements(errorType) {
		panic(fmt.Errorf("%#v isn't a func with error as its second return value", f))
	}
	returnType = fTyp.Out(0)
	for returnType.Kind() == reflect.Ptr {
		returnType = returnType.Elem()
	}
	if needType != nil && needType != returnType {
		panic(fmt.Errorf("%#v and %#v not the same resource type", needType, returnType))
	}
	return fVal, returnType
}

func GenerateJava(pkg string) (map[string]string, error) {
	classes := map[string]string{}
	for _, res := range resources {
		javaCode, err := res.toJavaInterface(pkg)
		if err != nil {
			return nil, err
		}
		classes[fmt.Sprintf("%sService.java", res.Type.Name())] = javaCode

		resClasses, err := res.toJavaClasses(pkg, "")
		if err != nil {
			return nil, err
		}
		for k, v := range resClasses {
			classes[k] = v
		}
	}
	return classes, nil
}

func DumpJava(dir string, classes map[string]string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	fs, err := file.Stat()
	if err != nil {
		return err
	}
	if !fs.IsDir() {
		return fmt.Errorf("generateJava requires a directory argument, %v is not a directory", dir)
	}
	for f, d := range classes {
		if err := ioutil.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.java", f)), []byte(d), 0644); err != nil {
			return err
		}
	}
	return nil
}
