(function () {
    'use strict';

    angular.module('ariaNg').controller('GatewayDestinationController', ['$rootScope', '$scope', 'ariaNgCommonService', 'transferGatewayService', function ($rootScope, $scope, ariaNgCommonService, transferGatewayService) {
        var emptyDestination = function () {
            return {
                id: '',
                name: '',
                provider: 'openlist',
                endpoint: '',
                mount: '/',
                remote: '',
                root: '/',
                rclone_config: '',
                token: '',
                proxy_enabled: false,
                proxy: ''
            };
        };
        var errorMessage = function (error, fallback) {
            return error && error.data && error.data.error ? error.data.error : fallback;
        };

        $scope.gatewayEnabled = transferGatewayService.isEnabled();
        $scope.destinations = [];
        $scope.editing = false;
        $scope.form = emptyDestination();

        $scope.loadDestinations = function () {
            if (!$scope.gatewayEnabled) {
                return;
            }
            var request = transferGatewayService.getDestinations().then(function (destinations) {
                $scope.destinations = angular.isArray(destinations) ? destinations : [];
                return $scope.destinations;
            }, function (error) {
                ariaNgCommonService.showError(errorMessage(error, 'Failed to load transfer destinations.'));
                throw error;
            });
            $rootScope.loadPromise = request;
            return request;
        };

        $scope.createDestination = function () {
            $scope.editing = false;
            $scope.form = emptyDestination();
        };

        $scope.editDestination = function (destination) {
            $scope.editing = true;
            $scope.form = angular.copy(destination);
            $scope.form.token = '';
            $scope.form.proxy_enabled = !!destination.has_proxy;
            if (destination.has_proxy_credentials) {
                $scope.form.proxy = '';
            }
        };

        $scope.cancelEdit = function () {
            $scope.createDestination();
        };

        $scope.saveDestination = function () {
            var destination = {
                id: $scope.form.id,
                name: $scope.form.name,
                provider: $scope.form.provider,
                endpoint: $scope.form.endpoint || '',
                mount: $scope.form.mount || '',
                remote: $scope.form.remote || '',
                root: $scope.form.root || '',
                rclone_config: $scope.form.rclone_config || '',
                token: $scope.form.token || '',
                proxy: $scope.form.proxy_enabled ? ($scope.form.proxy || '') : '',
                clear_proxy: !$scope.form.proxy_enabled
            };
            var operation = $scope.editing ?
                transferGatewayService.updateDestination(destination.id, destination) :
                transferGatewayService.createDestination(destination);
            $rootScope.loadPromise = operation.then(function () {
                $scope.createDestination();
                return $scope.loadDestinations();
            }, function (error) {
                ariaNgCommonService.showError(errorMessage(error, 'Failed to save transfer destination.'));
                throw error;
            });
            return $rootScope.loadPromise;
        };

        $scope.setDefault = function (destination) {
            if (!destination || destination.is_default) {
                return;
            }
            $rootScope.loadPromise = transferGatewayService.setDefaultDestination(destination.id).then(function () {
                return $scope.loadDestinations();
            }, function (error) {
                ariaNgCommonService.showError(errorMessage(error, 'Failed to set default destination.'));
                throw error;
            });
        };

        $scope.deleteDestination = function (destination) {
            if (!destination || destination.is_default) {
                return;
            }
            ariaNgCommonService.confirm('Confirm Delete', 'Delete this transfer destination?', 'warning', function () {
                $rootScope.loadPromise = transferGatewayService.deleteDestination(destination.id).then(function () {
                    if ($scope.editing && $scope.form.id === destination.id) {
                        $scope.createDestination();
                    }
                    return $scope.loadDestinations();
                }, function (error) {
                    ariaNgCommonService.showError(errorMessage(error, 'Failed to delete transfer destination.'));
                    throw error;
                });
            });
        };

        $scope.loadDestinations();
    }]);
}());
