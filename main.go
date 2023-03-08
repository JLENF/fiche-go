package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	// Loop infinito
	for {
		// Loop para receber conexões na porta 9999
		for {
			// Recebendo conexão
			listener, err := net.Listen("tcp", ":9999")
			if err != nil {
				fmt.Printf("Erro ao iniciar listener: %v\n", err)
				return
			}

			// Gerando nome aleatório para o diretório
			dirName := generateRandomString(4)

			// Criando diretório
			err = os.Mkdir(dirName, 0755)
			if err != nil {
				fmt.Printf("Erro ao criar diretório: %v\n", err)
				return
			}

			// Criando arquivo index.txt dentro do diretório
			file, err := os.Create(fmt.Sprintf("%s/index.txt", dirName))
			if err != nil {
				fmt.Printf("Erro ao criar arquivo: %v\n", err)
				return
			}
			defer file.Close()

			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("Erro ao aceitar conexão: %v\n", err)
				return
			}

			// Definindo tempo limite para leitura de dados
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))

			// Lendo texto recebido e salvando no arquivo
			textBytes, err := ioutil.ReadAll(conn)
			recvBytes, err := file.Write(textBytes)
			if err != nil {
				fmt.Printf("Erro ao escrever texto no arquivo: %v\n", err)
				return
			}

			// Exibindo nome do diretório onde o arquivo foi salvo e enviando para o cliente
			msg := fmt.Sprintf("Diretório criado: %s - bytes recebidos: %v\n", dirName, recvBytes)
			fmt.Print(msg)
			conn.Write([]byte(msg))

			// Fechando conexão
			conn.Close()
			listener.Close()
		}

		// Capturando sinal SIGINT (Ctrl+C) para interromper o loop
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nFinalizando o programa...")
		return
	}
}

// Função que gera uma string aleatória com n caracteres
func generateRandomString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
