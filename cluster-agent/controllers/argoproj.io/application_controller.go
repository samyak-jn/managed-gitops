/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package argoprojio

import (
	"context"
	"encoding/json"
	"fmt"

	appv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	database "github.com/redhat-appstudio/managed-gitops/backend-shared/config/db"
	util "github.com/redhat-appstudio/managed-gitops/backend-shared/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ApplicationReconciler reconciles a Application object
type ApplicationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Application object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile

func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	contextLog := log.FromContext(ctx)

	// creating databaseQueries object to fetch all the queries functionality
	databaseQueries, err := database.NewUnsafePostgresDBQueries(true, false)
	if err != nil {
		return ctrl.Result{}, err
	}

	// retrieve the application CR from the namespace and store the value in app object
	app := appv1.Application{}
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		contextLog.Error(err, "Unable to find Application: "+app.Name)

		// Todo: to decide what will be Application_id from Namespace Application CR perspective
		// To propose: fetch the list of application from the database and store it in an array, similarly if there is a way to fetch all the application in namespace find that
		// store that in an array, if the database.name dosen't exists in application namespace create an entry there! (But the follow up question being what if the Application Name is repicated,
		// should we set Application Name entry in database to be UNIQUE?)
		dbApplication := &database.Application{Application_id: string(app.UID)}

		errGetApp := databaseQueries.UnsafeGetApplicationById(ctx, dbApplication)
		// this proves that the the application CR is neither present in the namespace nor in the database
		if errGetApp != nil {
			return ctrl.Result{}, errGetApp
		}

		// Case 1: If nil, then Application CR is present in the database, hence create a new Application CR in the namespace
		application, errConvert := dbAppToApplicationCR(dbApplication)
		if errConvert != nil {
			return ctrl.Result{}, errConvert
		}
		errAddToNamespace := r.Create(ctx, application, &client.CreateOptions{})
		if errAddToNamespace != nil {
			return ctrl.Result{}, errAddToNamespace
		}

		// Update the application state after application is pushed to Namespace
		// if ApplicationState Row exists no change, else create!

		dbApplicationState := &database.ApplicationState{Applicationstate_application_id: string(app.UID)}
		errAppState := databaseQueries.UnsafeGetApplicationStateById(ctx, dbApplicationState)
		if errAppState != nil {
			dbApplicationState := &database.ApplicationState{
				Applicationstate_application_id: dbApplication.Application_id,
				Health:                          string(application.Status.Health.Status),
				Sync_Status:                     string(application.Status.Sync.Status),
			}

			errCreateApplicationState := databaseQueries.UnsafeCreateApplicationState(ctx, dbApplicationState)
			if errCreateApplicationState != nil {
				return ctrl.Result{}, errCreateApplicationState
			}
		}

	}

	argocdNamespace := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "argocd",
		},
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(&argocdNamespace), &argocdNamespace); err != nil {
		return ctrl.Result{}, err
	}

	managedEnv, err := util.UncheckedGetOrCreateManagedEnvironmentByNamespaceUID(ctx, argocdNamespace, databaseQueries, contextLog)

	clusterCreds := database.ClusterCredentials{}
	{
		var clusterCredsList []database.ClusterCredentials
		if err := databaseQueries.UnsafeListAllClusterCredentials(ctx, &clusterCredsList); err != nil {
			return ctrl.Result{}, err
		}

		if (clusterCreds == database.ClusterCredentials{}) {
			clusterCreds := database.ClusterCredentials{
				Host:                        "host",
				Kube_config:                 "kube_config",
				Kube_config_context:         "kube_config_context",
				Serviceaccount_bearer_token: "serviceaccount_bearer_token",
				Serviceaccount_ns:           "serviceaccount_ns",
			}
			if err := databaseQueries.CreateClusterCredentials(ctx, &clusterCreds); err != nil {
				return ctrl.Result{}, fmt.Errorf("unable to create cluster creds for managed env: %v", err)
			}
		}
	}
	gitopsEngineNamespace := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: argocdNamespace.Name,
		},
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(&gitopsEngineNamespace), &gitopsEngineNamespace); err != nil {
		return ctrl.Result{}, err
	}
	kubesystemNamespaceUID := "UID"

	gitopsEngineInstance, _, err := util.UncheckedGetOrCreateGitopsEngineInstanceByInstanceNamespaceUID(ctx, gitopsEngineNamespace, kubesystemNamespaceUID, clusterCreds, databaseQueries, contextLog)

	checkApp := database.Application{
		Name:                    app.Name,
		Engine_instance_inst_id: gitopsEngineInstance.Gitopsengineinstance_id,
		Managed_environment_id:  managedEnv.Managedenvironment_id,
	}

	var applications []database.Application
	applications = append(applications, checkApp)
	err = databaseQueries.UnsafeListAllApplications(ctx, &applications)
	if err != nil { // if err is nil then shows the occurance of application cr in database
		// no occurence of Application CR in database
		// Case 2: If Application CR dosen't exist in database, remove from the namespace as well
		if err != nil {
			if errors.IsNotFound(err) {
				errDel := r.Delete(ctx, &app, &client.DeleteAllOfOptions{})
				if errDel != nil {
					return ctrl.Result{}, fmt.Errorf("error, %v ", errDel)
				}

			}
		}
	}

	// Case 3: If Application CR exists in database + namspace, compare!
	specDetails, _ := json.Marshal(app.Spec)
	if checkApp.Spec_field != string(specDetails) {
		newSpecErr := json.Unmarshal([]byte(checkApp.Spec_field), &app.Spec)
		if newSpecErr != nil {
			return ctrl.Result{}, newSpecErr
		}

		errUpdate := r.Update(ctx, &app, &client.UpdateOptions{})
		if errUpdate != nil {
			return ctrl.Result{}, errUpdate
		}
	}
	contextLog.Info("Application event seen in reconciler: " + app.Name)

	return ctrl.Result{}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1.Application{}).
		Complete(r)
}

func dbAppToApplicationCR(dbApp *database.Application) (*appv1.Application, error) {

	newApplicationEntry := &appv1.Application{
		//  Todo: should come from gitopsengineinstance!
		ObjectMeta: metav1.ObjectMeta{Name: dbApp.Name, Namespace: "argocd"},
	}
	// assigning spec
	newSpecErr := json.Unmarshal([]byte(dbApp.Spec_field), &newApplicationEntry.Spec)
	if newSpecErr != nil {
		return nil, newSpecErr
	}

	return newApplicationEntry, nil
}
