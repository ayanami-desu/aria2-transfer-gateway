(function () {
    'use strict';

    angular.module('ariaNg').controller('NewTaskController', ['$rootScope', '$scope', '$location', '$timeout', 'ariaNgCommonService', 'ariaNgLogService', 'ariaNgKeyboardService', 'ariaNgFileService', 'ariaNgSettingService', 'aria2TaskService', 'aria2SettingService', 'transferGatewayService', function ($rootScope, $scope, $location, $timeout, ariaNgCommonService, ariaNgLogService, ariaNgKeyboardService, ariaNgFileService, ariaNgSettingService, aria2TaskService, aria2SettingService, transferGatewayService) {
        var tabStatusItems = [
            {
                name: 'links',
                show: true
            },
            {
                name: 'options',
                show: true
            }
        ];
        var parameters = $location.search();

        var getVisibleTabOrders = function () {
            var items = [];

            for (var i = 0; i < tabStatusItems.length; i++) {
                if (tabStatusItems[i].show) {
                    items.push(tabStatusItems[i].name);
                }
            }

            return items;
        };

        var setTabItemShow = function (name, status) {
            for (var i = 0; i < tabStatusItems.length; i++) {
                if (tabStatusItems[i].name === name) {
                    tabStatusItems[i].show = status;
                    break;
                }
            }
        };

        var saveDownloadPath = function (options) {
            if (!options || !options.dir) {
                return;
            }

            aria2SettingService.addSettingHistory('dir', options.dir);
        };

        var gatewayRequestFailed = function (error) {
            var message = 'Transfer gateway request failed';
            if (error && error.data && error.data.error) {
                message = error.data.error;
            }
            ariaNgCommonService.showError(message);
            return error;
        };

        var loadTransferDestinations = function () {
            if (!$scope.context.gatewayEnabled) {
                return;
            }

            $scope.context.destinationLoading = true;
            transferGatewayService.getDestinations().then(function (destinations) {
                $scope.context.destinations = destinations || [];
                $scope.context.destinationLoading = false;
                if (!$scope.context.destinationId && $scope.context.destinations.length > 0) {
                    $scope.context.destinationId = $scope.context.destinations[0].id;
                }
            }, function (error) {
                $scope.context.destinationLoading = false;
                $scope.context.destinationError = 'Unable to load transfer destinations';
                gatewayRequestFailed(error);
            });
        };

        var getDownloadTasksByLinks = function (options) {
            var urls = ariaNgCommonService.parseUrlsFromOriginInput($scope.context.urls);
            var tasks = [];

            if (!options) {
                options = angular.copy($scope.context.options);
            }

            for (var i = 0; i < urls.length; i++) {
                if (urls[i] === '' || urls[i].trim() === '') {
                    continue;
                }

                tasks.push({
                    urls: [urls[i].trim()],
                    options: options
                });
            }

            return tasks;
        };

        var downloadByLinks = function (pauseOnAdded, responseCallback) {
            var options = angular.copy($scope.context.options);
            var tasks = getDownloadTasksByLinks(options);

            saveDownloadPath(options);

            if ($scope.context.gatewayEnabled) {
                return transferGatewayService.createUriTasks(
                    tasks,
                    pauseOnAdded,
                    $scope.context.destinationId,
                    $scope.context.targetPath
                ).then(responseCallback, gatewayRequestFailed);
            }

            return aria2TaskService.newUriTasks(tasks, pauseOnAdded, responseCallback);
        };

        var downloadByTorrent = function (pauseOnAdded, responseCallback) {
            var options = angular.copy($scope.context.options);
            var content = $scope.context.uploadFile.base64Content;

            saveDownloadPath(options);

            if ($scope.context.gatewayEnabled) {
                return transferGatewayService.createContentTask(
                    'torrent',
                    content,
                    options,
                    pauseOnAdded,
                    $scope.context.destinationId,
                    $scope.context.targetPath
                ).then(responseCallback, gatewayRequestFailed);
            }

            return aria2TaskService.newTorrentTask({content: content, options: options}, pauseOnAdded, responseCallback);
        };

        var downloadByMetalink = function (pauseOnAdded, responseCallback) {
            var options = angular.copy($scope.context.options);
            var content = $scope.context.uploadFile.base64Content;

            saveDownloadPath(options);

            if ($scope.context.gatewayEnabled) {
                return transferGatewayService.createContentTask(
                    'metalink',
                    content,
                    options,
                    pauseOnAdded,
                    $scope.context.destinationId,
                    $scope.context.targetPath
                ).then(responseCallback, gatewayRequestFailed);
            }

            return aria2TaskService.newMetalinkTask({content: content, options: options}, pauseOnAdded, responseCallback);
        };

        $scope.context = {
            currentTab: 'links',
            taskType: 'urls',
            urls: '',
            uploadFile: null,
            gatewayEnabled: transferGatewayService.isEnabled(),
            destinations: [],
            destinationId: '',
            targetPath: '/',
            destinationLoading: false,
            destinationError: '',
            availableOptions: (function () {
                var keys = aria2SettingService.getNewTaskOptionKeys();

                return aria2SettingService.getSpecifiedOptions(keys, {
                    disableRequired: true
                });
            })(),
            globalOptions: null,
            options: {},
            optionFilter: {
                global: true,
                http: false,
                bittorrent: false
            },
            exportCommandApiOptions: null
        };
        loadTransferDestinations();

        if (parameters.url) {
            try {
                $scope.context.urls = ariaNgCommonService.base64UrlDecode(parameters.url);
            } catch (ex) {
                ariaNgLogService.error('[NewTaskController] base64 decode error, url=' + parameters.url, ex);
            }
        }

        $scope.changeTab = function (tabName) {
            if (tabName === 'options') {
                $scope.loadDefaultOption();
            }

            $scope.context.currentTab = tabName;
        };

        $rootScope.swipeActions.extendLeftSwipe = function () {
            var tabItems = getVisibleTabOrders();
            var tabIndex = tabItems.indexOf($scope.context.currentTab);

            if (tabIndex < tabItems.length - 1) {
                $scope.changeTab(tabItems[tabIndex + 1]);
                return true;
            } else {
                return false;
            }
        };

        $rootScope.swipeActions.extendRightSwipe = function () {
            var tabItems = getVisibleTabOrders();
            var tabIndex = tabItems.indexOf($scope.context.currentTab);

            if (tabIndex > 0) {
                $scope.changeTab(tabItems[tabIndex - 1]);
                return true;
            } else {
                return false;
            }
        };

        $scope.loadDefaultOption = function () {
            if ($scope.context.globalOptions) {
                return;
            }

            $rootScope.loadPromise = aria2SettingService.getGlobalOption(function (response) {
                if (response.success) {
                    $scope.context.globalOptions = response.data;
                }
            });
        };

        $scope.openTorrent = function () {
            ariaNgFileService.openFileContent({
                scope: $scope,
                fileFilter: '.torrent',
                fileType: 'binary'
            }, function (result) {
                $scope.context.uploadFile = result;
                $scope.context.taskType = 'torrent';
                $scope.changeTab('options');
            }, function (error) {
                ariaNgCommonService.showError(error);
            }, angular.element('#file-holder'));
        };

        $scope.openMetalink = function () {
            ariaNgFileService.openFileContent({
                scope: $scope,
                fileFilter: '.meta4,.metalink',
                fileType: 'binary'
            }, function (result) {
                $scope.context.uploadFile = result;
                $scope.context.taskType = 'metalink';
                $scope.changeTab('options');
            }, function (error) {
                ariaNgCommonService.showError(error);
            }, angular.element('#file-holder'));
        };

        $scope.isNewTaskValid = function () {
            if ($scope.context.gatewayEnabled && !$scope.context.destinationId) {
                return false;
            }
            if (!$scope.context.uploadFile) {
                return $scope.newTaskForm.$valid;
            }

            return true;
        };

        $scope.startDownload = function (pauseOnAdded) {
            var responseCallback = function (response) {
                if (!response.hasSuccess && !response.success) {
                    return;
                }

                var firstTask = null;

                if (response.results && response.results.length > 0) {
                    firstTask = response.results[0];
                } else if (response) {
                    firstTask = response;
                }

                if (ariaNgSettingService.getAfterCreatingNewTask() === 'task-detail' && firstTask && firstTask.data) {
                    $location.path('/task/detail/' + firstTask.data);
                } else {
                    if (pauseOnAdded) {
                        $location.path('/waiting');
                    } else {
                        $location.path('/downloading');
                    }
                }
            };

            if ($scope.context.taskType === 'urls') {
                $rootScope.loadPromise = downloadByLinks(pauseOnAdded, responseCallback);
            } else if ($scope.context.taskType === 'torrent') {
                $rootScope.loadPromise = downloadByTorrent(pauseOnAdded, responseCallback);
            } else if ($scope.context.taskType === 'metalink') {
                $rootScope.loadPromise = downloadByMetalink(pauseOnAdded, responseCallback);
            }
        };

        $scope.showExportCommandAPIModal = function () {
            $scope.context.exportCommandApiOptions = {
                type: 'new-task',
                data: getDownloadTasksByLinks()
            };
        };

        $scope.setOption = function (key, value, optionStatus) {
            if (value !== '' || !aria2SettingService.isOptionKeyRequired(key)) {
                $scope.context.options[key] = value;
            } else {
                delete $scope.context.options[key];
            }

            optionStatus.setReady();
        };

        $scope.urlTextboxKeyDown = function (event) {
            if (!ariaNgSettingService.getKeyboardShortcuts()) {
                return;
            }

            if (ariaNgKeyboardService.isCtrlEnterPressed(event) && $scope.newTaskForm.$valid) {
                if (event.preventDefault) {
                    event.preventDefault();
                }

                $scope.startDownload();

                return false;
            }
        };

        $scope.getValidUrlsCount = function () {
            var urls = ariaNgCommonService.parseUrlsFromOriginInput($scope.context.urls);
            return urls ? urls.length : 0;
        };

        $rootScope.loadPromise = $timeout(function () {}, 100);
    }]);
}());
