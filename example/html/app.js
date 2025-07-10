document.addEventListener('DOMContentLoaded', () => {
    // DOM元素
    const connectionStatus = document.getElementById('connection-status');
    const recordingStatus = document.getElementById('recording-status');
    const asrResult = document.getElementById('asr-result');
    const chatResult = document.getElementById('chat-result');
    const startBtn = document.getElementById('start-btn');
    const abortBtn = document.getElementById('abort-btn');

    // WebSocket连接
    let socket;
    let isConnected = false;
    let userInitiatedDisconnect = false;
    let isRecording = false;
    let ttsAudioContext;
    let audioQueue = [];
    let isPlaying = false;

    // 初始化TTS音频上下文
    function initTtsAudioContext() {
        ttsAudioContext = new (window.AudioContext || window.webkitAudioContext)({
            latencyHint: 'interactive' // 使用低延迟模式，不指定采样率让浏览器自动选择
        });
    }

    // 确保AudioContext处于运行状态
    function resumeAudioContext() {
        if (!ttsAudioContext) {
            initTtsAudioContext();
            return Promise.resolve();
        }
        if (ttsAudioContext.state === 'suspended') {
            console.log('恢复AudioContext');
            return ttsAudioContext.resume();
        }
        if (ttsAudioContext.state === 'closed') {
            console.log('重新初始化AudioContext');
            initTtsAudioContext();
            return Promise.resolve();
        }
        return Promise.resolve();
    }

    // 连接WebSocket服务器
    function connectWebSocket() {
        initTtsAudioContext();
        if (isConnected) return;

        socket = new WebSocket('ws://localhost:28080/crow/v1');

        socket.onopen = () => {
            console.log('WebSocket连接已建立');
            isConnected = true;
            connectionStatus.textContent = '已连接';
            connectionStatus.className = 'status-connected';
            sendHelloMessage();
        };

        socket.onmessage = (event) => {
            handleServerMessage(event.data);
        };

        socket.onerror = (error) => {
            console.error('WebSocket错误:', error);
            connectionStatus.textContent = '连接错误';
            connectionStatus.className = 'status-disconnected';
        };

        socket.onclose = () => {
            console.log('WebSocket连接已关闭');
            isConnected = false;
            connectionStatus.textContent = '未连接';
            connectionStatus.className = 'status-disconnected';
            // 如果不是用户主动断开，则自动重连
            if (!userInitiatedDisconnect) {
                setTimeout(connectWebSocket, 3000);
            }
        };
    }

    // 发送hello消息
    function sendHelloMessage() {
        if (!isConnected) return;

        const helloMessage = {
            type: 'hello',
            enable_asr: true,
            enable_tts: true,
            asr_params: {
                format: 'pcm',
                sample_rate: 16000,
                channels: 1,
                vad_eos: 1000,
                enable_punc: true,
                language: 'zh',
                accent: 'mandarin',
            },
            tts_params: {
                format: 'mp3',
                speed: 1.0,
                volume: 50,
                pitch: 1.0,
                sample_rate: 16000,
                language: 'zh'
            }
        };

        socket.send(JSON.stringify(helloMessage));
        console.log('发送hello消息:', helloMessage);
    }

    // 发送abort请求
    function sendAbortMessage() {
        if (!isConnected) return;

        const abortMessage = {
            type: 'abort'
        };

        socket.send(JSON.stringify(abortMessage));
        console.log('发送abort消息');
    }

    // 处理服务器消息
    function handleServerMessage(data) {
        try {
            const message = JSON.parse(data);
            console.log('收到服务器消息:', message);

            switch (message.type) {
                case 'asr':
                    handleAsrResponse(message);
                    break;
                case 'chat':
                    handleChatResponse(message);
                    break;
                case 'tts':
                    handleTtsResponse(message);
                    break;
                case 'error':
                    handleErrorResponse(message);
                    break;
                case 'hello':
                    break;
                default:
                    console.log('未知消息类型:', message.type);
            }
        } catch (error) {
            console.error('解析服务器消息失败:', error);
        }
    }

    // 处理ASR响应
    function handleAsrResponse(message) {
        if (message.result) {
            asrResult.textContent = message.result;
            // 清除原有对话响应
            chatResult.textContent = '';
        }
        // 根据状态可以添加额外处理
        if (message.state === 2) {
            console.log('ASR识别结束');
        }
    }

    // 处理Chat响应
    function handleChatResponse(message) {
        if (message.text) {
            chatResult.textContent += message.text;
        }
    }

    async function handleTtsResponse(message) {
        if (!message.audio) return;
        
        try {
            // 将base64音频数据转换为ArrayBuffer
            const audioData = Uint8Array.from(atob(message.audio), c => c.charCodeAt(0)).buffer;
            
            // 预解码音频以减少播放延迟
            ttsAudioContext.decodeAudioData(audioData).then(audioBuffer => {
                console.log('TTS音频解码成功，时长:', audioBuffer.duration);
                audioQueue.push(audioBuffer);
                
                // 确保音频上下文已激活并开始播放
                resumeAudioContext().then(() => {
                    console.log('音频上下文已激活，准备播放，队列长度:', audioQueue.length);
                    if (!isPlaying) {
                        playAudioQueue();
                    }
                }).catch(error => {
                    console.error('激活音频上下文失败:', error);
                    // 尝试HTML5 Audio备选播放
                    try {
                        const audio = new Audio();
                        audio.src = URL.createObjectURL(new Blob([audioData], {type: 'audio/mp3'}));
                        audio.play().then(() => {
                            console.log('HTML5 Audio备选播放成功');
                        }).catch(audioError => {
                            console.error('HTML5 Audio播放失败:', audioError);
                        });
                    } catch (finalError) {
                        console.error('所有音频播放方案均失败:', finalError);
                    }
                });
            }).catch(error => {
                console.error('TTS音频解码失败:', error);
                // 尝试使用备用音频上下文解码
                try {
                    const fallbackContext = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 16000 });
                    fallbackContext.decodeAudioData(audioData).then(fallbackBuffer => {
                        console.log('备用音频上下文解码成功');
                        audioQueue.push(fallbackBuffer);
                        resumeAudioContext().then(() => {
                            if (!isPlaying && audioQueue.length > 0) {
                                playAudioQueue();
                            }
                        });
                    }).catch(fallbackError => {
                        console.error('备用解码也失败:', fallbackError);
                        // 尝试直接播放原始数据
                        const audio = new Audio();
                        audio.src = URL.createObjectURL(new Blob([audioData], {type: 'audio/mp3'}));
                        audio.play().then(() => {
                            console.log('使用HTML5 Audio直接播放成功');
                        }).catch(audioError => {
                            console.error('所有播放方案均失败:', audioError);
                        });
                    });
                } catch (initError) {
                    console.error('创建备用音频上下文失败:', initError);
                    // 尝试直接播放原始音频数据作为备选方案
                    try {
                        const audio = new Audio();
                        audio.src = URL.createObjectURL(new Blob([audioData], {type: 'audio/mp3'}));
                        audio.play().then(() => {
                            console.log('使用HTML5 Audio直接播放成功');
                        }).catch(audioError => {
                            console.error('HTML5 Audio播放失败:', audioError);
                        });
                    } catch (fallbackError) {
                        console.error('备选播放方案失败:', fallbackError);
                    }
                }
            });
        } catch (error) {
            console.error('处理TTS音频失败:', error);
        }
    }

    async function playAudioQueue() {
        if (audioQueue.length === 0) {
            isPlaying = false;
            return;
        }
        
        isPlaying = true;
        const audioBuffer = audioQueue.shift();
        
        try {
            // 确保音频上下文已激活
            await resumeAudioContext();
            console.log('AudioContext状态:', ttsAudioContext.state);
            if (ttsAudioContext.state !== 'running') {
                throw new Error('AudioContext未成功激活，状态:' + ttsAudioContext.state);
            }
            
            if (!audioBuffer) {
                console.error('无效的音频缓冲区');
                isPlaying = false;
                playAudioQueue();
                return;
            }
            
            if (audioBuffer.sampleRate !== ttsAudioContext.sampleRate) {
                console.warn(`音频采样率不匹配: ${audioBuffer.sampleRate} vs ${ttsAudioContext.sampleRate}`);
            }
            
            // 创建音频源并连接到输出
            const source = ttsAudioContext.createBufferSource();
            source.buffer = audioBuffer;
            source.connect(ttsAudioContext.destination);
            
            // 播放结束后继续播放队列中的音频
            source.onended = () => {
                isPlaying = false;
                playAudioQueue();
            };
            
            // 使用低延迟模式播放
            source.start(0);
            source.playbackRate.value = 1.0;
            console.log('音频播放开始');
        } catch (error) {
            console.error('播放音频失败:', error);
            isPlaying = false;
            // 尝试播放下一个音频块
            playAudioQueue();
        }
    }

    async function startRecording() {
        if (isRecording || !isConnected) return;
        
        try {
            const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
            const audioContext = new AudioContext({ sampleRate: 16000 });
            const sourceNode = audioContext.createMediaStreamSource(stream);
            
            // 创建处理器节点用于实时处理音频数据
            const processorNode = audioContext.createScriptProcessor(1024, 1, 1);
            
            // 连接音频节点
            sourceNode.connect(processorNode);
            processorNode.connect(audioContext.destination);
            
            isRecording = true;
            recordingStatus.textContent = '正在录音...';
            startBtn.textContent = '停止录音';
            
            // 处理音频数据
            processorNode.onaudioprocess = (e) => {
                if (!isRecording) return;
                
                // 获取输入音频数据（单声道）
                const inputData = e.inputBuffer.getChannelData(0);
                
                // 将Float32Array转换为Int16Array（PCM格式）
                const pcmData = convertFloat32ToInt16(inputData);
                
                // 通过WebSocket发送音频数据（二进制消息）
                if (socket && socket.readyState === WebSocket.OPEN) {
                    socket.send(pcmData);
                }
            };
            
            // 保存录音相关对象以便停止时使用
            window.recordingContext = {
                audioContext,
                stream,
                processorNode,
                sourceNode
            };
            
        } catch (error) {
            console.error('开始录音失败:', error);
            alert('无法访问麦克风，请确保已授予权限并尝试再次开始。');
        }
    }

    // 将Float32音频数据转换为Int16 PCM格式
    function convertFloat32ToInt16(buffer) {
        const l = buffer.length;
        const buf = new Int16Array(l);
        for (let i = 0; i < l; i++) {
            buf[i] = Math.min(1, Math.max(-1, buffer[i])) < 0 ? 
                buffer[i] * 0x8000 : buffer[i] * 0x7FFF;
        }
        return buf.buffer;
    }

    // 停止录音
    function stopRecording() {
        if (!isRecording) return;
        
        const recordingContext = window.recordingContext;
        if (recordingContext) {
            // 停止音频处理
            if (recordingContext.processorNode) {
                recordingContext.processorNode.disconnect();
                recordingContext.processorNode.onaudioprocess = null;
            }
            
            // 停止媒体流
            if (recordingContext.stream) {
                recordingContext.stream.getTracks().forEach(track => track.stop());
            }
            
            // 关闭音频上下文
            if (recordingContext.audioContext) {
                recordingContext.audioContext.close();
            }
            
            // 清除保存的录音上下文
            window.recordingContext = null;
        }
        
        isRecording = false;
        recordingStatus.textContent = '录音已停止';
        startBtn.textContent = '开始录音';
    }

    // 处理错误响应
    function handleErrorResponse(message) {
        console.error('服务器错误:', message.error_msg);
        alert(`错误: ${message.error_msg}`);
        // 错误时停止录音
        if (isRecording) {
            stopRecording();
        }
    }

    // 断开WebSocket连接
    function disconnectWebSocket() {
        if (socket) {
            userInitiatedDisconnect = true;
            socket.close();
            isConnected = false;
            connectionStatus.textContent = '已断开';
            connectionStatus.className = 'status-disconnected';
            console.log('WebSocket连接已手动断开');
        }
    }

    // 事件监听
    startBtn.addEventListener('click', () => {
        resumeAudioContext(); // 用户交互时恢复音频上下文
        if (!isRecording) {
            startRecording();
        } else {
            stopRecording();
        }
    });

    abortBtn.addEventListener('click', () => {
        sendAbortMessage();
        stopRecording();
    });

    // 断开连接按钮事件
    document.getElementById('disconnect-btn').addEventListener('click', disconnectWebSocket);

    // 页面加载时添加用户交互监听以激活AudioContext
    document.addEventListener('click', function initialInteraction() {
        resumeAudioContext().then(() => {
            console.log('初始用户交互激活AudioContext');
        });
        document.removeEventListener('click', initialInteraction);
    }, { once: true });

    // 初始化连接
    connectWebSocket();
});