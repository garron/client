// Copyright © 2019 The Knative Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package revision

import (
	"testing"

	"gotest.tools/assert"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	"knative.dev/client/pkg/kn/commands"
	"knative.dev/client/pkg/util"
)

func fakeRevisionDelete(args []string) (action clienttesting.Action, name string, output string, err error) {
	knParams := &commands.KnParams{}
	cmd, fakeServing, buf := commands.CreateTestKnCommand(NewRevisionCommand(knParams), knParams)
	fakeServing.AddReactor("delete", "revisions",
		func(a clienttesting.Action) (bool, runtime.Object, error) {
			deleteAction, _ := a.(clienttesting.DeleteAction)
			action = deleteAction
			name = deleteAction.GetName()
			return true, nil, nil
		})
	cmd.SetArgs(args)
	err = cmd.Execute()
	if err != nil {
		return
	}
	output = buf.String()
	return
}

func TestRevisionDelete(t *testing.T) {
	revName := "foo-12345"
	action, name, output, err := fakeRevisionDelete([]string{"revision", "delete", revName})
	if err != nil {
		t.Error(err)
		return
	}
	if action == nil {
		t.Errorf("No action")
	} else if !action.Matches("delete", "revisions") {
		t.Errorf("Bad action %v", action)
	} else if name != revName {
		t.Errorf("Bad revision name returned after delete.")
	}
	assert.Check(t, util.ContainsAll(output, "Revision", revName, "deleted", "namespace", commands.FakeNamespace))
}

func TestMultipleRevisionDelete(t *testing.T) {
	revName1 := "foo-12345"
	revName2 := "foo-67890"
	revName3 := "foo-abcde"
	action, _, output, err := fakeRevisionDelete([]string{"revision", "delete", revName1, revName2, revName3})
	if err != nil {
		t.Error(err)
		return
	}
	if action == nil {
		t.Errorf("No action")
	} else if !action.Matches("delete", "revisions") {
		t.Errorf("Bad action %v", action)
	}
	assert.Check(t, util.ContainsAll(output, "Revision", revName1, "deleted", "namespace", commands.FakeNamespace))
	assert.Check(t, util.ContainsAll(output, "Revision", revName2, "deleted", "namespace", commands.FakeNamespace))
	assert.Check(t, util.ContainsAll(output, "Revision", revName3, "deleted", "namespace", commands.FakeNamespace))
}
