package goaeoas

import (
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/kr/pretty"
)

type Inner struct {
	POSTInteger  int    `methods:"POST"`
	POSTString   string `methods:"POST"`
	POSTIntSlice []int  `methods:"POST"`
	PUTInteger   int    `methods:"PUT"`
	PUTString    string `methods:"PUT"`
	PUTIntSlice  []int  `methods:"PUT"`
}

type Inner2 struct {
	POSTInteger2  int    `methods:"POST"`
	POSTString2   string `methods:"POST"`
	POSTIntSlice2 []int  `methods:"POST"`
	PUTInteger2   int    `methods:"PUT"`
	PUTString2    string `methods:"PUT"`
	PUTIntSlice2  []int  `methods:"PUT"`
}

type Inner3 struct {
	POSTInteger3  int    `methods:"POST"`
	POSTString3   string `methods:"POST"`
	POSTIntSlice3 []int  `methods:"POST"`
	PUTInteger3   int    `methods:"PUT"`
	PUTString3    string `methods:"PUT"`
	PUTIntSlice3  []int  `methods:"PUT"`
}

type Outer struct {
	Inner2             `methods:"POST"`
	Inner3             `methods:"PUT"`
	POSTSubStruct      Inner   `methods:"POST"`
	POSTInteger        int     `methods:"POST"`
	POSTString         string  `methods:"POST"`
	POSTIntSlice       []int   `methods:"POST"`
	POSTSubStructSlice []Inner `methods:"POST"`
	PUTSubStruct       Inner   `methods:"PUT"`
	PUTInteger         int     `methods:"PUT"`
	PUTString          string  `methods:"PUT"`
	PUTIntSlice        []int   `methods:"PUT"`
	PUTSubStructSlice  []Inner `methods:"PUT"`
}

var (
	data = `
{
    "POSTSubStruct": {
      "POSTInteger": 1,
      "POSTString": "a",
			"POSTIntSlice": [0,1,2],
      "PUTInteger": 2,
      "PUTString": "b",
			"PUTIntSlice": [3,4,5]
    },
    "POSTInteger": 3,
    "POSTString": "c",
		"POSTIntSlice": [6,7,8],
		"POSTSubStructSlice": [
		  {
				"POSTInteger": 101,
				"POSTString": "10a",
				"POSTIntSlice": [10,0,1,2],
				"PUTInteger": 102,
				"PUTString": "10b",
				"PUTIntSlice": [10,3,4,5]
			}
		],
    "PUTSubStruct": {
      "POSTInteger": 4,
      "POSTString": "d",
			"POSTIntSlice": [9,10,11],
      "PUTInteger": 5,
      "PUTString": "e",
			"PUTIntSlice": [12,13,14]
    },
    "PUTInteger": 6,
    "PUTString": "f",
		"PUTIntSlice": [15,16,17],
		"PUTSubStructSlice": [
		  {
				"POSTInteger": 201,
				"POSTString": "20a",
				"POSTIntSlice": [20,0,1,2],
				"PUTInteger": 202,
				"PUTString": "20b",
				"PUTIntSlice": [20,3,4,5]
			}
		],
	  "POSTInteger2": 7,
		"POSTString2": "g",
		"POSTIntSlice2": [18,19,20],
		"PUTInteger2": 8,
		"PUTString2": "h",
		"POSTIntSlice2": [21,22,23],
		"POSTInteger3": 9,
		"POSTString3": "i",
		"POSTIntSlice3": [24,25,26],
		"PUTInteger3": 10,
		"PUTString3": "j",
		"PUTIntSlice3": [27,28,29]
  }
`
	expectedPOSTOuter = &Outer{
		POSTSubStruct: Inner{
			POSTInteger:  1,
			POSTString:   "a",
			POSTIntSlice: []int{0, 1, 2},
		},
		POSTInteger:  3,
		POSTString:   "c",
		POSTIntSlice: []int{6, 7, 8},
		POSTSubStructSlice: []Inner{
			{
				POSTInteger:  101,
				POSTString:   "10a",
				POSTIntSlice: []int{10, 0, 1, 2},
			},
		},
		Inner2: Inner2{
			POSTInteger2:  7,
			POSTString2:   "g",
			POSTIntSlice2: []int{21, 22, 23},
		},
	}
	expectedPUTOuter = &Outer{
		PUTSubStruct: Inner{
			PUTInteger:  5,
			PUTString:   "e",
			PUTIntSlice: []int{12, 13, 14},
		},
		PUTInteger:  6,
		PUTString:   "f",
		PUTIntSlice: []int{15, 16, 17},
		PUTSubStructSlice: []Inner{
			{
				PUTInteger:  202,
				PUTString:   "20b",
				PUTIntSlice: []int{20, 3, 4, 5},
			},
		},
		Inner3: Inner3{
			PUTInteger3:  10,
			PUTString3:   "j",
			PUTIntSlice3: []int{27, 28, 29},
		},
	}
)

func TestCopyJSON(t *testing.T) {
	outer := &Outer{}
	if err := copyJSON(outer, []byte(data), "POST"); err != nil {
		t.Fatal(err)
	}
	if diff := pretty.Diff(outer, expectedPOSTOuter); len(diff) > 0 {
		t.Errorf("Wrong copy result, got %v, want %v; diff %v", spew.Sdump(outer), spew.Sdump(expectedPOSTOuter), spew.Sdump(diff))
	}
	outer = &Outer{}
	if err := copyJSON(outer, []byte(data), "PUT"); err != nil {
		t.Fatal(err)
	}
	if diff := pretty.Diff(outer, expectedPUTOuter); len(diff) > 0 {
		t.Errorf("Wrong copy result, got %v, want %v; diff %v", spew.Sdump(outer), spew.Sdump(expectedPUTOuter), spew.Sdump(diff))
	}
}
